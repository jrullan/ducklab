package bus

import (
	"sync"
	"testing"
	"time"
)

func drain(sub *Subscriber, d time.Duration) []Event {
	var got []Event
	deadline := time.After(d)
	for {
		select {
		case e, ok := <-sub.Ch:
			if !ok {
				return got
			}
			got = append(got, e)
		case <-deadline:
			return got
		}
	}
}

func TestPublishRespectsFilter(t *testing.T) {
	b := New(16)
	sub, unsub := b.Subscribe("s1", func(e Event) bool { return e.RunID == "r-1" })
	defer unsub()

	b.Publish(Event{Type: "a", RunID: "r-1", Seq: 1})
	b.Publish(Event{Type: "b", RunID: "r-2", Seq: 2})
	b.Publish(Event{Type: "c", RunID: "r-1", Seq: 3})

	got := drain(sub, 50*time.Millisecond)
	if len(got) != 2 {
		t.Fatalf("got %d events, want 2: %+v", len(got), got)
	}
	if got[0].Seq != 1 || got[1].Seq != 3 {
		t.Errorf("wrong events delivered: %+v", got)
	}
}

func TestPublishPreservesOrder(t *testing.T) {
	b := New(256)
	sub, unsub := b.Subscribe("s", nil)
	defer unsub()

	for i := 1; i <= 100; i++ {
		b.Publish(Event{Type: "e", RunID: "r", Seq: i})
	}
	got := drain(sub, 200*time.Millisecond)
	if len(got) != 100 {
		t.Fatalf("got %d events, want 100", len(got))
	}
	for i, e := range got {
		if e.Seq != i+1 {
			t.Fatalf("out of order at %d: seq %d", i, e.Seq)
		}
	}
}

// A subscriber that stops reading must be dropped with an overflow marker,
// never allowed to block the publisher. I11: a slow client cannot stall a run.
func TestSlowSubscriberOverflowsInsteadOfBlocking(t *testing.T) {
	b := New(4)
	sub, unsub := b.Subscribe("slow", nil)
	defer unsub()

	done := make(chan struct{})
	go func() {
		for i := 0; i < 1000; i++ {
			b.Publish(Event{Type: "e", RunID: "r", Seq: i})
		}
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Publish blocked on a subscriber that stopped reading")
	}

	got := drain(sub, 50*time.Millisecond)
	sawOverflow := false
	for _, e := range got {
		if e.Type == "overflow" {
			sawOverflow = true
		}
	}
	if !sawOverflow {
		t.Error("no overflow event delivered to the slow subscriber")
	}
}

func TestConcurrentPublishAndSubscribe(t *testing.T) {
	b := New(256)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			sub, unsub := b.Subscribe(string(rune('a'+n)), nil)
			defer unsub()
			drain(sub, 30*time.Millisecond)
		}(i)
	}
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				b.Publish(Event{Type: "e", Seq: j})
			}
		}(i)
	}
	wg.Wait() // -race makes this meaningful
}

// After an overflow the reader must see the channel END, not block forever.
//
// Without the close, the SSE handler sat on a channel nobody would publish to
// again: the HTTP connection stayed open but permanently silent, and the
// client never learned to reconnect. A run's conversation simply stopped
// growing at the point the client fell behind.
func TestOverflowClosesTheChannelSoReadersTerminate(t *testing.T) {
	b := New(2)
	sub, unsub := b.Subscribe("slow", nil)
	defer unsub()

	for i := 0; i < 100; i++ {
		b.Publish(Event{Type: "e", RunID: "r", Seq: i})
	}

	// Drain everything the subscriber holds; the channel must then be closed.
	closed := false
	deadline := time.After(2 * time.Second)
	for !closed {
		select {
		case _, open := <-sub.Ch:
			if !open {
				closed = true
			}
		case <-deadline:
			t.Fatal("channel never closed after overflow; a reader would block forever")
		}
	}
}

func TestOverflowMarkerArrivesBeforeTheClose(t *testing.T) {
	b := New(2)
	sub, unsub := b.Subscribe("slow", nil)
	defer unsub()

	for i := 0; i < 100; i++ {
		b.Publish(Event{Type: "e", RunID: "r", Seq: i})
	}
	sawOverflow := false
	for {
		e, open := <-sub.Ch
		if !open {
			break
		}
		if e.Type == "overflow" {
			sawOverflow = true
		}
	}
	if !sawOverflow {
		t.Error("the subscriber was closed without ever being told to resync")
	}
}
