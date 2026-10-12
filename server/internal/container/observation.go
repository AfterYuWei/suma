package container

import (
	"context"
	"errors"
	"io"
	"regexp"
	"time"
)

var ErrObservationInput = errors.New("invalid container observation request")
var containerIdentifier = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,127}$`)
var fullContainerID = regexp.MustCompile(`^[a-f0-9]{64}$`)

type HealthCheckRecord struct {
	StartedAt       time.Time `json:"started_at"`
	FinishedAt      time.Time `json:"finished_at"`
	ExitCode        int       `json:"exit_code"`
	Output          string    `json:"output"`
	OutputRedacted  bool      `json:"output_redacted"`
	OutputTruncated bool      `json:"output_truncated"`
}
type HealthDetails struct {
	Configured      *bool               `json:"configured"`
	FailingStreak   *int                `json:"failing_streak"`
	LastCheckedAt   *time.Time          `json:"last_checked_at"`
	Command         *string             `json:"command"`
	IntervalMS      *int64              `json:"interval_ms"`
	TimeoutMS       *int64              `json:"timeout_ms"`
	StartPeriodMS   *int64              `json:"start_period_ms"`
	StartIntervalMS *int64              `json:"start_interval_ms"`
	Retries         *int                `json:"retries"`
	Logs            []HealthCheckRecord `json:"logs"`
}
type Diagnostics struct {
	Runtime       RuntimeMetadata `json:"runtime"`
	RestartPolicy *string         `json:"restart_policy"`
	Health        HealthDetails   `json:"health"`
}
type ContainerEvent struct {
	ID           string    `json:"id"`
	NodeID       string    `json:"node_id"`
	ContainerID  string    `json:"container_id"`
	Time         time.Time `json:"time"`
	TimeNano     string    `json:"time_nano"`
	Action       string    `json:"action"`
	ExitCode     *int      `json:"exit_code"`
	Signal       string    `json:"signal,omitempty"`
	HealthStatus string    `json:"health_status,omitempty"`
}
type EventQuery struct {
	Since, Until time.Time
	Limit        int
}
type EventHistory struct {
	Events         []ContainerEvent `json:"events"`
	Source         string           `json:"source"`
	Coverage       string           `json:"coverage"`
	RequestedSince time.Time        `json:"requested_since"`
	RequestedUntil time.Time        `json:"requested_until"`
	Truncated      bool             `json:"truncated"`
}
type ObservationProvider interface {
	Diagnostics(context.Context, string) (Diagnostics, error)
	ContainerEvents(context.Context, string, EventQuery, func(ContainerEvent) error) error
	NormalizedStats(context.Context, string) (io.ReadCloser, error)
}

func ReadDiagnostics(ctx context.Context, p ObservationProvider, id string) (Diagnostics, error) {
	if !containerIdentifier.MatchString(id) {
		return Diagnostics{}, ErrObservationInput
	}
	ctx, cancel := context.WithTimeout(ctx, RuntimeReadTimeout)
	defer cancel()
	return p.Diagnostics(ctx, id)
}
func ValidateEventQuery(id string, q EventQuery, live bool) error {
	if !fullContainerID.MatchString(id) || q.Since.IsZero() || q.Since.After(time.Now().Add(time.Minute)) {
		return ErrObservationInput
	}
	if live {
		return nil
	}
	if q.Until.IsZero() || q.Until.Before(q.Since) || q.Until.Sub(q.Since) > 24*time.Hour || q.Until.After(time.Now().Add(time.Minute)) || q.Limit < 1 || q.Limit > 256 {
		return ErrObservationInput
	}
	return nil
}
func ReadContainerEvents(ctx context.Context, p ObservationProvider, nodeID, id string, q EventQuery) (EventHistory, error) {
	result := EventHistory{Events: []ContainerEvent{}, Source: "docker", Coverage: "engine_retained", RequestedSince: q.Since, RequestedUntil: q.Until}
	if err := ValidateEventQuery(id, q, false); err != nil {
		return result, err
	}
	ctx, cancel := context.WithTimeout(ctx, RuntimeReadTimeout)
	defer cancel()
	err := p.ContainerEvents(ctx, id, q, func(e ContainerEvent) error { e.NodeID = nodeID; result.Events = append(result.Events, e); return nil })
	if len(result.Events) > q.Limit {
		result.Truncated = true
		result.Events = result.Events[len(result.Events)-q.Limit:]
	}
	return result, err
}
func WatchContainerEvents(ctx context.Context, p ObservationProvider, nodeID, id string, since time.Time, consume func(ContainerEvent) error) error {
	q := EventQuery{Since: since}
	if err := ValidateEventQuery(id, q, true); err != nil {
		return err
	}
	return p.ContainerEvents(ctx, id, q, func(e ContainerEvent) error { e.NodeID = nodeID; return consume(e) })
}
func OpenNormalizedStats(ctx context.Context, p ObservationProvider, id string) (io.ReadCloser, error) {
	if !containerIdentifier.MatchString(id) {
		return nil, ErrObservationInput
	}
	return p.NormalizedStats(ctx, id)
}
