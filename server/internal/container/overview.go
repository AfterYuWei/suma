package container

import (
	"context"
)

// Overview contains only the bounded topology needed by the quick view.
// Runtime status and trend samples come from the existing list reads.
type Overview struct {
	ID          string            `json:"id"`
	NetworkMode *string           `json:"network_mode"`
	Ports       []OverviewPort    `json:"ports"`
	Networks    []OverviewNetwork `json:"networks"`
	Mounts      []OverviewMount   `json:"mounts"`
	Counts      OverviewCounts    `json:"counts"`
}
type OverviewCounts struct {
	Networks int `json:"networks"`
	Mounts   int `json:"mounts"`
	Ports    int `json:"ports"`
}
type OverviewNetwork struct {
	Name        string `json:"name"`
	IPAddress   string `json:"ip_address,omitempty"`
	IPv6Address string `json:"ipv6_address,omitempty"`
}
type OverviewMount struct {
	Type        string `json:"type"`
	Name        string `json:"name,omitempty"`
	Source      string `json:"source,omitempty"`
	Destination string `json:"destination"`
	ReadWrite   bool   `json:"read_write"`
	Anonymous   *bool  `json:"anonymous,omitempty"`
}
type OverviewPort struct {
	Port
	HostBinding bool `json:"host_binding"`
}
type OverviewProvider interface {
	ContainerOverview(context.Context, string) (Overview, error)
}

func ValidateOverviewID(id string) error {
	if !containerIdentifier.MatchString(id) {
		return ErrObservationInput
	}
	return nil
}
func ReadOverview(ctx context.Context, p OverviewProvider, id string) (Overview, error) {
	if err := ValidateOverviewID(id); err != nil {
		return Overview{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, RuntimeReadTimeout)
	defer cancel()
	return p.ContainerOverview(ctx, id)
}
