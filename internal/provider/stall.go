package provider

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"
)

// stallWatch is the stall watchdog every streaming provider runs. An upstream
// that accepts the request and then sends NOTHING held a run for the full
// 300s client timeout — and the retry chain behind it re-ran the same wait
// three more times, so one stalled provider cost twenty silent minutes
// (T-075, twice, two models). A healthy stream delivers its first byte in
// seconds and never goes quiet for two minutes between chunks; one that does
// is weather, and weather is retried — fast.
type stallWatch struct {
	stall    time.Duration
	activity chan struct{}
	stalled  atomic.Bool
}

// watchStall returns a context cancelled after stall of silence; touch
// resets the clock. Cancel the returned func when the stream is done.
func watchStall(ctx context.Context, stall time.Duration) (context.Context, *stallWatch, context.CancelFunc) {
	if stall <= 0 {
		stall = defaultStallTimeout
	}
	ctx, cancel := context.WithCancel(ctx)
	w := &stallWatch{stall: stall, activity: make(chan struct{}, 1)}
	go func() {
		t := time.NewTimer(stall)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-w.activity:
				if !t.Stop() {
					select {
					case <-t.C:
					default:
					}
				}
				t.Reset(stall)
			case <-t.C:
				w.stalled.Store(true)
				cancel()
				return
			}
		}
	}()
	return ctx, w, cancel
}

func (w *stallWatch) touch() {
	select {
	case w.activity <- struct{}{}:
	default:
	}
}

func (w *stallWatch) tripped() bool { return w.stalled.Load() }

// err dresses a watchdog trip as what it is: the provider's silence,
// transient, retryable — not a mysterious context error.
func (w *stallWatch) err(err error) error {
	if w.tripped() {
		return fmt.Errorf("%w: provider sent nothing for %s (stalled stream)", ErrProviderUnavailable, w.stall)
	}
	return err
}
