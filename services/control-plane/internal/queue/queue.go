// Package queue manages the NATS JetStream topology for the execution
// plane: the durable job stream, the workers' pull consumer and the
// domain-event stream.
//
// Delivery semantics: jobs use an explicit-ack durable pull consumer with
// bounded redelivery; terminal failures are moved to the DLQ subject by
// the worker. Every business event is published to the events stream for
// observability and webhook fan-out.
package queue

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

// Subject and stream naming for the platform.
const (
	// SubjectJobs carries execution jobs (one per queued execution).
	SubjectJobs = "agentos.executions.jobs"
	// SubjectDLQ carries jobs that exhausted their redelivery budget.
	SubjectDLQ = "agentos.executions.dlq"
	// SubjectEventsPrefix is the base subject for domain events:
	// agentos.events.<organization_id>.<type>
	SubjectEventsPrefix = "agentos.events"

	// StreamJobs is the durable execution job stream.
	StreamJobs = "EXECUTIONS"
	// StreamEvents is the domain event stream (observability, webhooks).
	StreamEvents = "EVENTS"
	// ConsumerWorkers is the durable pull consumer used by runtime workers.
	ConsumerWorkers = "execution-workers"
)

// Job is the execution job payload published to SubjectJobs. The Python
// worker deserializes this contract (app/engine/job.py mirrors it).
type Job struct {
	ExecutionID    string          `json:"execution_id"`
	OrganizationID string          `json:"organization_id"`
	AgentID        string          `json:"agent_id"`
	AgentVersionID string          `json:"agent_version_id"`
	DeploymentID   string          `json:"deployment_id,omitempty"`
	IdentityID     string          `json:"identity_id,omitempty"`
	Input          json.RawMessage `json:"input"`
	SubmittedAt    time.Time       `json:"submitted_at"`
	Attempt        int             `json:"attempt"`
}

// EnsureStream creates or updates the JetStream streams and the worker
// consumer. It is idempotent and safe to run at every startup.
func EnsureStream(ctx context.Context, js jetstream.JetStream, logger *slog.Logger) error {
	jobsCfg := jetstream.StreamConfig{
		Name:      StreamJobs,
		Subjects:  []string{SubjectJobs, SubjectDLQ},
		Retention: jetstream.LimitsPolicy,
		MaxAge:    7 * 24 * time.Hour,
		MaxMsgs:   1_000_000,
		Storage:   jetstream.FileStorage,
	}
	if _, err := js.CreateOrUpdateStream(ctx, jobsCfg); err != nil {
		return fmt.Errorf("ensure %s stream: %w", StreamJobs, err)
	}

	eventsCfg := jetstream.StreamConfig{
		Name:      StreamEvents,
		Subjects:  []string{SubjectEventsPrefix + ".>"},
		Retention: jetstream.LimitsPolicy,
		MaxAge:    24 * time.Hour,
		MaxMsgs:   2_000_000,
		Storage:   jetstream.FileStorage,
	}
	if _, err := js.CreateOrUpdateStream(ctx, eventsCfg); err != nil {
		return fmt.Errorf("ensure %s stream: %w", StreamEvents, err)
	}

	_, err := js.CreateOrUpdateConsumer(ctx, StreamJobs, jetstream.ConsumerConfig{
		Name:          ConsumerWorkers,
		Durable:       ConsumerWorkers,
		FilterSubject: SubjectJobs,
		AckPolicy:     jetstream.AckExplicitPolicy,
		AckWait:       2 * time.Minute,
		MaxDeliver:    6,
		MaxAckPending: 256,
	})
	if err != nil {
		return fmt.Errorf("ensure %s consumer: %w", ConsumerWorkers, err)
	}
	logger.Info("jetstream topology ensured",
		slog.String("jobs_stream", StreamJobs),
		slog.String("events_stream", StreamEvents),
		slog.String("consumer", ConsumerWorkers),
	)
	return nil
}

// Publisher wraps a NATS connection for platform publishing.
type Publisher struct {
	conn *nats.Conn
	js   jetstream.JetStream
}

// Connect establishes the NATS connection (and blocks until connected).
func Connect(ctx context.Context, url string) (*Publisher, error) {
	conn, err := nats.Connect(url,
		nats.Name("agentos-control-plane"),
		nats.MaxReconnects(-1),
		nats.ReconnectWait(2*time.Second),
	)
	if err != nil {
		return nil, fmt.Errorf("connect nats: %w", err)
	}
	js, err := jetstream.New(conn)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("create jetstream context: %w", err)
	}
	return &Publisher{conn: conn, js: js}, nil
}

// Close drains the connection.
func (p *Publisher) Close() { p.conn.Close() }

// JetStream exposes the JetStream management context.
func (p *Publisher) JetStream() jetstream.JetStream { return p.js }

// PublishJob enqueues an execution job. The message ID is the execution
// ID + attempt so JetStream deduplicates redelivery pairs.
func (p *Publisher) PublishJob(ctx context.Context, job Job) error {
	payload, err := json.Marshal(job)
	if err != nil {
		return fmt.Errorf("marshal job: %w", err)
	}
	msgID := job.ExecutionID + ":" + fmt.Sprint(job.Attempt)
	if _, err := p.js.PublishMsg(ctx, &nats.Msg{
		Subject: SubjectJobs,
		Data:    payload,
		Header:  nats.Header{"Nats-Msg-Id": []string{msgID}},
	}); err != nil {
		return fmt.Errorf("publish job: %w", err)
	}
	return nil
}

// PublishEvent emits a domain event on the events stream.
func (p *Publisher) PublishEvent(ctx context.Context, organizationID, eventType string, payload []byte) error {
	subject := fmt.Sprintf("%s.%s.%s", SubjectEventsPrefix, organizationID, eventType)
	if _, err := p.js.PublishMsg(ctx, &nats.Msg{
		Subject: subject,
		Data:    payload,
		Header:  nats.Header{"Nats-Msg-Id": []string{subject + ":" + uuidString()}},
	}); err != nil {
		return fmt.Errorf("publish event: %w", err)
	}
	return nil
}

// Healthy reports JetStream availability for readiness endpoints.
func (p *Publisher) Healthy(ctx context.Context) error {
	_, err := p.js.AccountInfo(ctx)
	return err
}

func uuidString() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return fmt.Sprintf("%x", b[:])
}
