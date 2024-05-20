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
	Endpoint  *Endpoint
	Result    chan struct {
		Container *Container
		Endpoint  *Endpoint
	}
}

type ActionRunner struct {
	pingerCallback func()
	addCallback    func(*Container)
	updateCallback func(*Container, *Endpoint)
	removeCallback func(*Container)
	getCallback    func(string, string) (*Container, *Endpoint)

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

func (ar *ActionRunner) Update(container *Container, endpoint *Endpoint) {
	ar.push(&Event{Type: updateEvent, Container: container, Endpoint: endpoint})
}

func (ar *ActionRunner) Remove(container *Container) {
	ar.push(&Event{Type: removeEvent, Container: container})
}

func (ar *ActionRunner) Get(ctx context.Context, endpoint *Endpoint) (*Container, *Endpoint) {
	evt := &Event{
		Type:     getEvent,
		Endpoint: endpoint,
		Result: make(chan struct {
			Container *Container
			Endpoint  *Endpoint
		}, 1),
	}

	ar.push(evt)

	select {
	case r := <-evt.Result:
		return r.Container, r.Endpoint
	case <-ctx.Done():
		return nil, nil
	case <-ar.close:
		return nil, nil
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

func WithUpdateCallback(callback func(*Container, *Endpoint)) func(*ActionRunner) {
	return func(ar *ActionRunner) {
		ar.updateCallback = callback
	}
}

func WithRemoveCallback(callback func(*Container)) func(*ActionRunner) {
	return func(ar *ActionRunner) {
		ar.removeCallback = callback
	}
}

func WithGetCallback(callback func(string, string) (*Container, *Endpoint)) func(*ActionRunner) {
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

		for {
			select {
			case <-ar.close:
				return
			case event, ok := <-ar.events:
				if !ok {
					return
				}

				switch event.Type {
				case pingerEvent:
					ar.pingerCallback()
				case addEvent:
					ar.addCallback(event.Container)
				case updateEvent:
					ar.updateCallback(event.Container, event.Endpoint)
				case removeEvent:
					ar.removeCallback(event.Container)
				case getEvent:
					container, endpoint := ar.getCallback(event.Endpoint.Domain, event.Endpoint.Path)
					event.Result <- struct {
						Container *Container
						Endpoint  *Endpoint
					}{container, endpoint}
				default:
					continue
				}
			}
		}
	}()

	return ar
}
