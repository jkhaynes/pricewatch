package pipeline

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/jkhaynes/pricewatch/internal/card"
	"github.com/jkhaynes/pricewatch/internal/priority"
)

type Store interface {
	StartRun(ctx context.Context) (int64, error)
	Candidates(ctx context.Context, source string) ([]card.Candidate, error)
	Save(ctx context.Context, runID int64, obs card.Observation) error
	PutMapping(ctx context.Context, m card.Mapping) error
	FinishRun(ctx context.Context, runID int64, ok, failed int) error
	Changes(ctx context.Context, runID int64) ([]card.Change, error)
}

type Runner struct {
	Store      Store
	Source     PriceSource
	SourceName string
	Budget     int // requests, i.e. source cards
	Workers    int
	Log        *slog.Logger
	Policy     priority.Policy  // which cards are due (DD-12); the zero Policy makes every card due
	Now        func() time.Time // nil: time.Now
}

func (r *Runner) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

type Summary struct {
	RunID           int64
	Requests        int
	Keys            int
	OK              int
	Failed          int
	Abandoned       int
	Deferred        int   // not priced this run, not a failure: stays stalest for next run
	NotDue          int   // priceable cards not due yet: left for a later run on purpose (DD-12)
	StoppedBy       error // ErrRateLimited or ErrQuotaExhausted, if the provider ended the run
	NewlyUnresolved []card.Mapping
	Changes         []card.Change
}

func (r *Runner) Run(ctx context.Context, stop <-chan struct{}) (Summary, error) {
	runID, err := r.Store.StartRun(ctx)
	if err != nil {
		return Summary{}, fmt.Errorf("start run: %w", err)
	}
	cands, err := r.Store.Candidates(ctx, r.SourceName)
	if err != nil {
		return Summary{}, fmt.Errorf("load candidates: %w", err)
	}
	picked, dueCount := priority.Due(cands, r.now(), r.Budget, r.Policy)
	var due []card.Mapping
	for _, c := range picked {
		due = append(due, c.Keys...)
	}
	jobs := group(due)
	sum := Summary{RunID: runID, Keys: len(due), NotDue: len(cands) - dueCount}
	r.Log.Info("run started", "run", runID, "requests", len(jobs), "keys", len(due),
		"due", dueCount, "not_due", sum.NotDue, "workers", r.Workers)

	// halt stops dispatch; either the caller's stop or a provider limit can trigger it.
	halted := make(chan struct{})
	var once sync.Once
	halt := func() { once.Do(func() { close(halted) }) }
	defer halt()
	go func() {
		select {
		case <-stop:
			halt()
		case <-halted:
		}
	}()

	persist := context.WithoutCancel(ctx) // finished work is saved even after Ctrl-C
	for res := range Work(ctx, r.Source, Feed(ctx, halted, jobs), r.Workers) {
		sum.Requests++
		r.handle(ctx, persist, runID, res, &sum, halt)
	}
	sum.Deferred = sum.Keys - sum.OK - sum.Failed - sum.Abandoned

	if err := r.Store.FinishRun(persist, runID, sum.OK, sum.Failed+sum.Abandoned); err != nil {
		r.Log.Error("finish run", "run", runID, "err", err)
	}
	if sum.Changes, err = r.Store.Changes(persist, runID); err != nil {
		r.Log.Error("load changes", "run", runID, "err", err)
	}
	return sum, nil
}

// group turns stalest-ordered mappings (grouped by source card) into one job per card.
func group(due []card.Mapping) []Job {
	var jobs []Job
	for _, m := range due {
		if n := len(jobs); n == 0 || jobs[n-1].SourceCardID != m.SourceCardID {
			jobs = append(jobs, Job{SourceCardID: m.SourceCardID})
		}
		last := &jobs[len(jobs)-1]
		last.Targets = append(last.Targets, Target{Key: m.Key, Variant: m.Variant})
	}
	return jobs
}

func (r *Runner) handle(ctx, persist context.Context, runID int64, res Result, sum *Summary, halt func()) {
	n, err := len(res.Job.Targets), res.Err
	switch {
	case errors.Is(err, card.ErrRateLimited), errors.Is(err, card.ErrQuotaExhausted):
		if sum.StoppedBy == nil {
			sum.StoppedBy = err
			r.Log.Warn("provider limit reached, stopping dispatch; remaining cards stay queued for next run", "err", err)
		}
		halt()
		return // its keys fall into Deferred
	case errors.Is(err, card.ErrNotFound):
		for _, t := range res.Job.Targets {
			r.unresolve(persist, t.Key, card.StatusUnmatched, err, sum)
		}
		return
	case ctx.Err() != nil && errors.Is(err, context.Canceled):
		r.Log.Info("abandoned in flight", "card", res.Job.SourceCardID)
		sum.Abandoned += n
		return
	case err != nil:
		r.Log.Warn("price request failed, skipping", "card", res.Job.SourceCardID, "err", err)
		sum.Failed += n
		return
	case len(res.Quotes) != n:
		r.Log.Error("provider returned wrong number of quotes", "card", res.Job.SourceCardID, "want", n, "got", len(res.Quotes))
		sum.Failed += n
		return
	}

	now := r.now()
	for i, q := range res.Quotes {
		key := res.Job.Targets[i].Key
		switch {
		case q.Err == nil:
			obs := card.Observation{CardID: key, Source: r.SourceName, Price: q.Price, ObservedAt: now}
			if err := r.Store.Save(persist, runID, obs); err != nil {
				r.Log.Error("save observation", "card", key, "err", err)
				sum.Failed++
				continue
			}
			sum.OK++
		case errors.Is(q.Err, card.ErrVariantAmbiguous):
			r.unresolve(persist, key, card.StatusAmbiguous, q.Err, sum)
		default: // ErrVariantUnavailable or anything else variant-specific
			r.unresolve(persist, key, card.StatusUnmatched, q.Err, sum)
		}
	}
}

func (r *Runner) unresolve(persist context.Context, key string, st card.Status, cause error, sum *Summary) {
	m := card.Mapping{Key: key, Source: r.SourceName, Status: st, Reason: cause.Error()}
	if err := r.Store.PutMapping(persist, m); err != nil {
		r.Log.Error("record unresolved", "card", key, "err", err)
	}
	r.Log.Warn("variant not resolvable, removed from rotation", "card", key, "status", st, "reason", m.Reason)
	sum.NewlyUnresolved = append(sum.NewlyUnresolved, m)
	sum.Failed++
}
