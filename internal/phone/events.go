package phone

import (
	"sync"

	"github.com/yibaiba/hideck/pkg/logger"
)

const (
	eventBufferSize      = 256
	subscriberBufferSize = 32
)

type eventHub struct {
	mu          sync.Mutex
	nextID      uint64
	events      []Event
	nextSubID   uint64
	subscribers map[uint64]eventSubscriber
}

type eventSubscriber struct {
	stream chan Event
	filter func(Event) bool
}

func newEventHub() *eventHub {
	return &eventHub{subscribers: make(map[uint64]eventSubscriber)}
}

func (hub *eventHub) publish(event Event) Event {
	hub.mu.Lock()
	defer hub.mu.Unlock()
	hub.nextID++
	event.ID = hub.nextID
	hub.events = append(hub.events, event)
	if len(hub.events) > eventBufferSize {
		hub.events = append([]Event(nil), hub.events[len(hub.events)-eventBufferSize:]...)
	}
	if event.Type == "call_answered" || event.Type == "call_ended" {
		// 诊断：DB 已正确落接通/结束，但前端偶发停在"响铃中/通话中"。
		// 记录发布瞬间的订阅者数，区分"SSE 已断（订阅者 0）"与"有订阅但
		// 前端未消费"。
		logger.Info("通话事件发布", "type", event.Type, "call_id", event.Call.CallID,
			"event_id", event.ID, "subscribers", len(hub.subscribers),
			"status", event.Call.Status)
	}
	for id, subscriber := range hub.subscribers {
		if subscriber.filter != nil && !subscriber.filter(event) {
			continue
		}
		select {
		case subscriber.stream <- event:
		default:
			delete(hub.subscribers, id)
			close(subscriber.stream)
		}
	}
	return event
}

func (hub *eventHub) subscribe(afterID uint64) ([]Event, <-chan Event, func()) {
	return hub.subscribeFiltered(afterID, nil)
}

func (hub *eventHub) subscribeFiltered(afterID uint64, filter func(Event) bool) ([]Event, <-chan Event, func()) {
	hub.mu.Lock()
	defer hub.mu.Unlock()
	if afterID > hub.nextID {
		// Cursor is from a previous process (IDs restart at 1). Replay the live buffer.
		afterID = 0
	}
	backlog := make([]Event, 0, len(hub.events))
	for _, event := range hub.events {
		if event.ID > afterID && (filter == nil || filter(event)) {
			backlog = append(backlog, event)
		}
	}
	hub.nextSubID++
	id := hub.nextSubID
	stream := make(chan Event, subscriberBufferSize)
	hub.subscribers[id] = eventSubscriber{stream: stream, filter: filter}
	return backlog, stream, func() {
		hub.mu.Lock()
		if subscriber, ok := hub.subscribers[id]; ok {
			delete(hub.subscribers, id)
			close(subscriber.stream)
		}
		hub.mu.Unlock()
	}
}
