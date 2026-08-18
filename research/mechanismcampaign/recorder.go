package mechanismcampaign

import (
	"fmt"
	"sync"
	"time"
)

type eventRecorder struct {
	mu     sync.Mutex
	origin time.Time
	events []Event
}

func newEventRecorder() *eventRecorder {
	return &eventRecorder{origin: time.Now()}
}

func (recorder *eventRecorder) record(event Event) Event {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	event.Sequence = uint64(len(recorder.events) + 1)
	event.ID = fmt.Sprintf("event-%03d", event.Sequence)
	event.AtNS = time.Since(recorder.origin).Nanoseconds()
	if event.AtNS < 0 {
		event.AtNS = 0
	}
	recorder.events = append(recorder.events, event)
	return event
}

func (recorder *eventRecorder) snapshot() []Event {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	return append([]Event(nil), recorder.events...)
}
