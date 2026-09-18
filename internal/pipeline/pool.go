package pipeline

import (
	"context"
	"sync"

	"github.com/jkhaynes/pricewatch/internal/card"
)

// Target is one collection key to price, and which print it is.
type Target struct {
	Key     string
	Variant card.Variant
}

// Job is one request's worth of work: every wanted variant of one source card.
type Job struct {
	SourceCardID string
	Targets      []Target
}

type Result struct {
	Job    Job
	Quotes []card.Quote // Quotes[i] answers Job.Targets[i]
	Err    error        // the request as a whole failed
}

// PriceSource is declared here, where it is consumed.
type PriceSource interface {
	Quote(ctx context.Context, sourceCardID string, variants []card.Variant) ([]card.Quote, error)
}

// Feed is the only thing that knows where jobs come from (DD-2). In phase 3 a
// RabbitMQ consumer takes its place and Work does not change.
func Feed(ctx context.Context, stop <-chan struct{}, jobs []Job) <-chan Job {
	out := make(chan Job)
	go func() {
		defer close(out)
		for _, j := range jobs {
			select { // check stop first so a ready worker cannot win the race after stop
			case <-stop:
				return
			case <-ctx.Done():
				return
			default:
			}
			select {
			case out <- j:
			case <-stop:
				return
			case <-ctx.Done():
				return
			}
		}
	}()
	return out
}

func Work(ctx context.Context, src PriceSource, jobs <-chan Job, workers int) <-chan Result {
	out := make(chan Result)
	var wg sync.WaitGroup
	for range workers {
		wg.Go(func() {
			for j := range jobs {
				variants := make([]card.Variant, len(j.Targets))
				for i, t := range j.Targets {
					variants[i] = t.Variant
				}
				qs, err := src.Quote(ctx, j.SourceCardID, variants)
				out <- Result{Job: j, Quotes: qs, Err: err}
			}
		})
	}
	go func() {
		wg.Wait()
		close(out)
	}()
	return out
}
