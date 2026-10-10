// Package event defines application events without coupling domain services to delivery.
package event

import "time"

type Event struct {
	ID           string    `json:"id"`
	Type         string    `json:"type"`
	Severity     string    `json:"severity"`
	Scope        string    `json:"scope"`
	NodeID       string    `json:"node_id,omitempty"`
	NodeName     string    `json:"node_name,omitempty"`
	ResourceType string    `json:"resource_type,omitempty"`
	ResourceID   string    `json:"resource_id,omitempty"`
	Project      string    `json:"project,omitempty"`
	Title        string    `json:"title"`
	Message      string    `json:"message"`
	TaskID       string    `json:"task_id,omitempty"`
	ReleaseID    *uint     `json:"release_id,omitempty"`
	ActorID      *uint     `json:"actor_id,omitempty"`
	DedupeKey    string    `json:"-"`
	Time         time.Time `json:"time"`
}
type Sink func(Event)
