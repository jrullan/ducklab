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
		defer b.mu.Unlock()
		delete(b.subscribers, id)
		close(sub.Ch)
	}
}

// Publish publishes an event to all matching subscribers.
// Non-blocking: if a subscriber's buffer is full, the event is dropped
// and the subscriber is sent an overflow event.
func (b *Bus) Publish(e Event) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	for _, sub := range b.subscribers {
		if sub.Filter != nil && !sub.Filter(e) {
			continue
		}
		select {
		case sub.Ch <- e:
		default:
			// Buffer full: drop the subscriber with an overflow event
			select {
			case sub.Ch <- Event{Type: "overflow", TS: time.Now()}:
			default:
			}
		}
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
		close(sub.Ch)
	}
	b.subscribers = make(map[string]*Subscriber)
}
