// Package events is the in-process event bus: a Watermill GoChannel pub/sub
// driven by a Watermill router. Messages only announce state that is already
// durable in SQLite, so a lost message delays work until the next sweep but
// never loses it.
package events

import (
	"context"
	"encoding/json"
	"log"
	"log/slog"
	"slices"

	"github.com/ThreeDotsLabs/watermill"
	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/ThreeDotsLabs/watermill/pubsub/gochannel"
)

type Bus struct {
	pubsub *gochannel.GoChannel
	router *message.Router
}

func New() (*Bus, error) {
	// Watermill logs every handler start at Info; keep those at Debug.
	logger := watermill.NewSlogLoggerWithLevelMapping(slog.Default(), map[slog.Level]slog.Level{slog.LevelInfo: slog.LevelDebug})
	router, err := message.NewRouter(message.RouterConfig{}, logger)
	if err != nil {
		return nil, err
	}
	return &Bus{pubsub: gochannel.NewGoChannel(gochannel.Config{OutputChannelBuffer: 64}, logger), router: router}, nil
}

// Publish sends payload, encoded as JSON, to every subscriber of topic.
func (b *Bus) Publish(topic string, payload any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return b.pubsub.Publish(topic, message.NewMessage(watermill.NewUUID(), data))
}

// Subscribe adds a handler for topic; name must be unique. Every subscription
// receives every message, and Watermill runs each message in its own
// goroutine, so a handler may run concurrently with itself and must bound its
// own concurrency. A failing or panicking handler is logged and the message
// acknowledged: redelivering it in a loop would not help, since the state it
// announced stays in SQLite.
func Subscribe[T any](b *Bus, name, topic string, handle func(ctx context.Context, event T) error) {
	b.router.AddConsumerHandler(name, topic, b.pubsub, func(msg *message.Message) error {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("events: %s: panic: %v", name, r)
			}
		}()
		var event T
		if err := json.Unmarshal(msg.Payload, &event); err != nil {
			log.Printf("events: %s: bad payload: %v", name, err)
			return nil
		}
		if err := handle(msg.Context(), event); err != nil {
			log.Printf("events: %s: %v", name, err)
		}
		return nil
	})
}

// Listen relays topic's events to a short-lived listener, such as a browser
// following a job, until ctx is cancelled. The relay takes each event as it
// comes, so nothing piles up behind a slow listener (Watermill holds a
// goroutine for every message its subscriber has not taken): an event the
// listener has not taken yet is kept once, however often it comes again, so it
// gets each distinct event that came since it last took that one. Delivery is
// not ordered, so treat each event as a cue to read the state from SQLite.
func Listen[T comparable](ctx context.Context, b *Bus, topic string) (<-chan T, error) {
	msgs, err := b.pubsub.Subscribe(ctx, topic)
	if err != nil {
		return nil, err
	}
	out := make(chan T)
	go func() {
		defer close(out)
		var waiting []T // not yet taken, each once, oldest first
		for {
			var send chan<- T // nil, so never chosen, while nothing waits
			var next T
			if len(waiting) > 0 {
				send, next = out, waiting[0]
			}
			select {
			case msg, ok := <-msgs: // closed when ctx is cancelled
				if !ok {
					return
				}
				msg.Ack()
				var event T
				if json.Unmarshal(msg.Payload, &event) == nil && !slices.Contains(waiting, event) {
					waiting = append(waiting, event)
				}
			case send <- next:
				waiting = waiting[1:]
			case <-ctx.Done():
				return
			}
		}
	}()
	return out, nil
}

// Run starts every subscription and blocks until ctx is cancelled and the
// handlers in flight have returned.
func (b *Bus) Run(ctx context.Context) error {
	defer b.pubsub.Close()
	return b.router.Run(ctx)
}

// Running is closed once all subscriptions are live.
func (b *Bus) Running() <-chan struct{} { return b.router.Running() }
