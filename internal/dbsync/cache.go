// Package dbsync keeps a local copy of the collection database fresh for the
// MCP server (DD-15). It downloads a new copy when the old one is older than a
// TTL, and swaps the open handle without pulling the file out from under a
// query that is still reading it.
package dbsync

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Cache hands out an open T for a local file, downloading a new copy of the
// file when it is older than TTL. The file's mtime is its age.
type Cache[T io.Closer] struct {
	Path  string                                       // the local copy
	TTL   time.Duration                                // how long a copy counts as fresh
	Fetch func(ctx context.Context, w io.Writer) error // writes a new copy; nil means never download
	Open  func(ctx context.Context, path string) (T, error)
	Now   func() time.Time // nil means time.Now

	refreshing sync.Mutex // one refresh at a time
	retryAt    time.Time  // after a failed download, no retry before this; guarded by refreshing

	mu     sync.RWMutex // read-locked by every Acquire until release; write-locked by a swap
	cur    T
	opened bool
	stale  string // why the copy may be out of date; "" when it is fresh
	err    error  // why there is no copy to hand out
}

// Acquire refreshes the copy if it is due, then returns the open value under a
// read lock. The caller must call release when done with v, and a swap waits
// for every release. stale explains why the copy may be out of date.
func (c *Cache[T]) Acquire(ctx context.Context) (v T, release func(), stale string, err error) {
	// Refresh's error is recorded in c.stale or c.err, which are reported below.
	_ = c.Refresh(ctx)
	c.mu.RLock()
	if !c.opened {
		err := c.err
		c.mu.RUnlock()
		return v, nil, "", err
	}
	return c.cur, c.mu.RUnlock, c.stale, nil
}

// Refresh downloads a new copy if the local one is missing or older than TTL,
// unless a download failed less than a TTL ago. It opens the copy if nothing
// is open yet.
func (c *Cache[T]) Refresh(ctx context.Context) error {
	c.refreshing.Lock()
	defer c.refreshing.Unlock()
	now := c.now()
	info, statErr := os.Stat(c.Path)
	have := statErr == nil
	due := !have || now.Sub(info.ModTime()) >= c.TTL
	if c.Fetch == nil || !due || now.Before(c.retryAt) {
		if !have {
			return c.fail(fmt.Errorf("no local copy at %s: %w", c.Path, statErr))
		}
		return c.openIfClosed(ctx)
	}
	tmp, err := c.download(ctx)
	if err != nil {
		c.retryAt = now.Add(c.TTL)
		if !have {
			return c.fail(fmt.Errorf("no local copy, and the download failed: %w", err))
		}
		c.mu.Lock()
		c.stale = fmt.Sprintf("refresh failed, so this is the copy from %s: %v",
			info.ModTime().UTC().Format(time.RFC3339), err)
		c.mu.Unlock()
		return errors.Join(err, c.openIfClosed(ctx))
	}
	c.retryAt = time.Time{}
	return c.swap(ctx, tmp, now)
}

// Close closes the open copy, if any.
func (c *Cache[T]) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.opened {
		return nil
	}
	c.opened = false
	return c.cur.Close()
}

func (c *Cache[T]) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

// fail records that there is no copy to hand out.
func (c *Cache[T]) fail(err error) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.err = err
	return err
}

func (c *Cache[T]) openIfClosed(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.opened {
		return nil
	}
	v, err := c.Open(ctx, c.Path)
	if err != nil {
		c.err = fmt.Errorf("open %s: %w", c.Path, err)
		return c.err
	}
	c.cur, c.opened, c.err = v, true, nil
	return nil
}

// download writes a new copy to a temp file beside Path, so the rename that
// replaces Path stays on one volume.
func (c *Cache[T]) download(ctx context.Context) (string, error) {
	dir := filepath.Dir(c.Path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create %s: %w", dir, err)
	}
	f, err := os.CreateTemp(dir, "download-*.db")
	if err != nil {
		return "", fmt.Errorf("create temp file: %w", err)
	}
	err = c.Fetch(ctx, f)
	if cerr := f.Close(); err == nil && cerr != nil {
		err = fmt.Errorf("write %s: %w", f.Name(), cerr)
	}
	if err != nil {
		// Best effort: a leftover temp file is harmless and the next download
		// makes a new one.
		os.Remove(f.Name())
		return "", err
	}
	return f.Name(), nil
}

// swap closes the open copy, moves the new one into place and opens it. The
// write lock waits until every tool call holding a read lock has released it.
func (c *Cache[T]) swap(ctx context.Context, tmp string, now time.Time) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	var closeErr error
	if c.opened {
		// Closed before the rename: Windows cannot replace a file that is open.
		closeErr = c.cur.Close()
		c.opened = false
	}
	if err := os.Rename(tmp, c.Path); err != nil {
		c.err = fmt.Errorf("replace %s: %w", c.Path, err)
		return errors.Join(closeErr, c.err)
	}
	// Path's mtime is the copy's age, so set it from the clock that judges it.
	if err := os.Chtimes(c.Path, now, now); err != nil {
		closeErr = errors.Join(closeErr, fmt.Errorf("stamp %s: %w", c.Path, err))
	}
	v, err := c.Open(ctx, c.Path)
	if err != nil {
		c.err = fmt.Errorf("open %s: %w", c.Path, err)
		return errors.Join(closeErr, c.err)
	}
	c.cur, c.opened, c.stale, c.err = v, true, "", nil
	return closeErr
}
