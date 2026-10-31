package webhooks

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

// SubjectEventsPrefix mirrors the queue package's event subject scheme.
const SubjectEventsPrefix = "agentos.events"

// Consumer subscribes to the platform event stream and hands each event
// to the dispatcher. It runs until the context is cancelled.
func (d *Dispatcher) ConsumeEvents(ctx context.Context, js jetstream.JetStream, logger *slog.Logger) error {
	consumer, err := js.CreateOrUpdateConsumer(ctx, "EVENTS", jetstream.ConsumerConfig{
		Name:          "webhook-dispatcher",
		Durable:       "webhook-dispatcher",
		FilterSubject: SubjectEventsPrefix + ".>",
		AckPolicy:     jetstream.AckExplicitPolicy,
		AckWait:       5 * time.Minute,
		MaxDeliver:    3,
	})
	if err != nil {
		return err
	}
	cc, err := consumer.Consume(func(msg jetstream.Msg) {
		parts := strings.Split(msg.Subject(), ".")
		// agentos.events.<org>.<event...>
		if len(parts) < 4 {
			_ = msg.Ack()
			return
		}
		orgID := parts[2]
		eventType := strings.Join(parts[3:], ".")
		var meta map[string]any
		_ = json.Unmarshal(msg.Data(), &meta)
		eventID, _ := meta["execution_id"].(string)
		if eventID == "" {
			eventID = msg.Subject()
		}
		if err := d.DeliverEvent(ctx, orgID, eventID, eventType, msg.Data()); err != nil {
			logger.Warn("webhook dispatch failed", slog.Any("error", err))
		}
		_ = msg.Ack()
	})
	if err != nil {
		return err
	}
	logger.Info("webhook dispatcher consuming platform events")
	go func() {
		<-ctx.Done()
		cc.Stop()
	}()
	return nil
}
