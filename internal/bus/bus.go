// Package bus provides an in-process event bus for fan-out of run events
// to SSE subscribers. The bus is the single point where all events flow.
package bus

import (
	"sync"
	"time"
)

// Event is a bus event.
type Event struct {
	Type      string                 `json:"type"`
	RunID     string                 `json:"run_id,omitempty"`
	ProjectID string                 `json:"project_id,omitempty"`
	Seq       int                    `json:"seq,omitempty"`
	TS        time.Time              `json:"ts"`
	Data      map[string]interface{} `json:"data,omitempty"`
}

// Subscriber receives events.
type Subscriber struct {
	ID     string
	Ch     chan Event
	Filter func(Event) bool

	// mu guards send-vs-close on Ch: a subscriber can be dropped for overflow
	// concurrently with its own unsubscribe, and sending on a closed channel
	// would panic and take the engine with it.
	mu         sync.Mutex
	closed     bool
	overflowed bool
}

// send delivers an event. It reports true exactly once, on the first
// overflow, so the caller drops the subscriber a single time.
func (s *Subscriber) send(e Event) (firstOverflow bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.overflowed {
		return false
	}
	select {
	case s.Ch <- e:
		return false
	default:
		s.overflowed = true
		return true
	}
}

// overflow discards the oldest buffered event to make room for a final
// overflow marker, so the client always learns it must resync. Without
// making room the marker itself is dropped and the client silently
// believes it is still up to date.
func (s *Subscriber) overflow() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	select {
	case <-s.Ch:
	default:
	}
	select {
	case s.Ch <- Event{Type: "overflow", TS: time.Now()}:
	default:
	}
}

func (s *Subscriber) close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.closed {
		s.closed = true
		close(s.Ch)
	}
}

// Bus is the event bus.
type Bus struct {
	mu          sync.RWMutex
	subscribers map[string]*Subscriber
	bufferSize  int
}

// New creates a new bus.
func New(bufferSize int) *Bus {
	if bufferSize <= 0 {
		bufferSize = 256
	}
	return &Bus{
		subscribers: make(map[string]*Subscriber),
		bufferSize:  bufferSize,
	}
}

// Subscribe adds a subscriber. Returns a function to unsubscribe.
func (b *Bus) Subscribe(id string, filter func(Event) bool) (*Subscriber, func()) {
	b.mu.Lock()
	defer b.mu.Unlock()
	sub := &Subscriber{
		ID:     id,
		Ch:     make(chan Event, b.bufferSize),
		Filter: filter,
	}
	b.subscribers[id] = sub
	return sub, func() {
		b.mu.Lock()
		if cur, ok := b.subscribers[id]; ok && cur == sub {
			delete(b.subscribers, id)
		}
		b.mu.Unlock()
		sub.close()
	}
}

// Publish publishes an event to all matching subscribers.
//
// Never blocks: a subscriber whose buffer is full is dropped from the bus
// with a final overflow event, so one slow client can never stall a run (I11).
func (b *Bus) Publish(e Event) {
	var dropped []*Subscriber

	b.mu.RLock()
	for _, sub := range b.subscribers {
		if sub.Filter != nil && !sub.Filter(e) {
			continue
		}
		if sub.send(e) {
			dropped = append(dropped, sub)
		}
	}
	b.mu.RUnlock()

	// Removal takes the write lock, so it happens after the read lock is
	// released.
	for _, sub := range dropped {
		sub.overflow()
		b.mu.Lock()
		if cur, ok := b.subscribers[sub.ID]; ok && cur == sub {
			delete(b.subscribers, sub.ID)
		}
		b.mu.Unlock()
	}
}

// PublishTokenDelta publishes a token delta (not persisted).
func (b *Bus) PublishTokenDelta(runID string, turn int, duckling, text string) {
	b.Publish(Event{
		Type:  "token_delta",
		RunID: runID,
		TS:    time.Now(),
		Data: map[string]interface{}{
			"turn":     turn,
			"duckling": duckling,
			"text":     text,
		},
	})
}

// PublishHeartbeat publishes a heartbeat (not persisted).
func (b *Bus) PublishHeartbeat() {
	b.Publish(Event{
		Type: "heartbeat",
		TS:   time.Now(),
	})
}

// Close closes the bus and all subscribers.
func (b *Bus) Close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, sub := range b.subscribers {
		sub.close()
	}
	b.subscribers = make(map[string]*Subscriber)
}
