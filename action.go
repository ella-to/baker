package baker

import (
	"context"
	"log/slog"
)

type EventType int

const (
	_ EventType = iota
	pingerEvent
	addEvent
	updateEvent
	removeEvent
	getEvent
)

type Event struct {
	Type      EventType
	Container *Container
	Domain    string
	Path      string
	Result    chan *Container
}

type ActionRunner struct {
	pingerCallback func()
	addCallback    func(*Container)
	updateCallback func(*Container, string, string)
	removeCallback func(*Container)
	getCallback    func(string, string) *Container

	events chan *Event
	close  chan struct{} // using this to make sure pushing to events stops when Close() is called
}

var _ Driver = (*ActionRunner)(nil)

func (ar *ActionRunner) Pinger() {
	ar.push(&Event{Type: pingerEvent})
}

func (ar *ActionRunner) Add(container *Container) {
	ar.push(&Event{Type: addEvent, Container: container})
}

func (ar *ActionRunner) Update(container *Container, domain string, path string) {
	ar.push(&Event{Type: updateEvent, Container: container, Domain: domain, Path: path})
}

func (ar *ActionRunner) Remove(container *Container) {
	ar.push(&Event{Type: removeEvent, Container: container})
}

func (ar *ActionRunner) Get(ctx context.Context, domain, path string) *Container {
	evt := &Event{
		Type:   getEvent,
		Domain: domain,
		Path:   path,
		Result: make(chan *Container, 1),
	}

	ar.push(evt)

	select {
	case container := <-evt.Result:
		return container
	case <-ctx.Done():
		return nil
	case <-ar.close:
		return nil
	}
}

func (ar *ActionRunner) push(event *Event) {
	select {
	case <-ar.close:
		return
	case ar.events <- event:
	default:
		slog.Error("ActionRunner: events channel is full, dropping event")
	}
}

func (ar *ActionRunner) Close() {
	close(ar.close)
	close(ar.events)
}

func WithPingerCallback(callback func()) func(*ActionRunner) {
	return func(ar *ActionRunner) {
		ar.pingerCallback = callback
	}
}

func WithAddCallback(callback func(*Container)) func(*ActionRunner) {
	return func(ar *ActionRunner) {
		ar.addCallback = callback
	}
}

func WithUpdateCallback(callback func(*Container, string, string)) func(*ActionRunner) {
	return func(ar *ActionRunner) {
		ar.updateCallback = callback
	}
}

func WithRemoveCallback(callback func(*Container)) func(*ActionRunner) {
	return func(ar *ActionRunner) {
		ar.removeCallback = callback
	}
}

func WithGetCallback(callback func(string, string) *Container) func(*ActionRunner) {
	return func(ar *ActionRunner) {
		ar.getCallback = callback
	}
}

type ActionCallback func(*ActionRunner)

func NewActionRunner(bufferSize int, cbs ...ActionCallback) *ActionRunner {
	ar := &ActionRunner{
		events: make(chan *Event, bufferSize),
		close:  make(chan struct{}),
	}

	for _, cb := range cbs {
		cb(ar)
	}

	go func() {
		defer slog.Debug("ActionRunner: stopped")

		for event := range ar.events {
			switch event.Type {
			case pingerEvent:
				ar.pingerCallback()
			case addEvent:
				ar.addCallback(event.Container)
			case updateEvent:
				ar.updateCallback(event.Container, event.Domain, event.Path)
			case removeEvent:
				ar.removeCallback(event.Container)
			case getEvent:
				event.Result <- ar.getCallback(event.Domain, event.Path)
			default:
				continue
			}
		}
	}()

	return ar
}
