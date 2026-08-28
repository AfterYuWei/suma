package project

import "time"

type Backend string

const (
	BackendCompose Backend = "compose"
	BackendSwarm   Backend = "swarm"
)

type ScopeKind string

const (
	ScopeEngine ScopeKind = "engine"
	ScopeSwarm  ScopeKind = "swarm"
)

type Capability string

const (
	CapabilityView          Capability = "view"
	CapabilityEdit          Capability = "edit"
	CapabilityDeploy        Capability = "deploy"
	CapabilityStart         Capability = "start"
	CapabilityStop          Capability = "stop"
	CapabilityRestart       Capability = "restart"
	CapabilityUpdate        Capability = "update"
	CapabilityDelete        Capability = "delete"
	CapabilityServices      Capability = "services"
	CapabilityLogs          Capability = "logs"
	CapabilityNetworks      Capability = "networks"
	CapabilityVolumes       Capability = "volumes"
	CapabilityTakeover      Capability = "takeover"
	CapabilityCleanup       Capability = "cleanup"
	CapabilityShadowPreview Capability = "shadow_preview"
)

type Scope struct {
	Kind ScopeKind `json:"kind"`
	ID   string    `json:"id"`
}

type Ref struct {
	Backend    Backend `json:"backend"`
	Scope      Scope   `json:"scope"`
	NativeName string  `json:"native_name"`
}

// Summary is the backend-neutral Project list model. Backend-specific detail
// stays in its backend package and is never forced into this common shape.
type Summary struct {
	Ref           Ref          `json:"ref"`
	Backend       Backend      `json:"backend"`
	Scope         Scope        `json:"scope"`
	NodeID        string       `json:"node_id,omitempty"`
	Name          string       `json:"name"`
	NativeName    string       `json:"native_name"`
	Managed       bool         `json:"managed"`
	Source        string       `json:"source"`
	Status        string       `json:"status"`
	Capabilities  []Capability `json:"capabilities"`
	ServiceCount  int          `json:"service_count"`
	InstanceCount int          `json:"instance_count"`
	CreatedAt     time.Time    `json:"created_at"`
	UpdatedAt     time.Time    `json:"updated_at"`
}

func ComposeRef(nodeID, name string) Ref {
	return Ref{Backend: BackendCompose, Scope: Scope{Kind: ScopeEngine, ID: nodeID}, NativeName: name}
}

func ComposeSummary(nodeID, name, source, status string, managed bool) Summary {
	ref := ComposeRef(nodeID, name)
	return Summary{
		Ref: ref, Backend: ref.Backend, Scope: ref.Scope, NodeID: nodeID,
		Name: name, NativeName: name, Managed: managed, Source: source,
		Status: status, Capabilities: ComposeCapabilities(managed),
	}
}

func ComposeCapabilities(managed bool) []Capability {
	if !managed {
		return []Capability{CapabilityView, CapabilityServices, CapabilityTakeover, CapabilityCleanup}
	}
	return []Capability{
		CapabilityView, CapabilityEdit, CapabilityDeploy, CapabilityStart,
		CapabilityStop, CapabilityRestart, CapabilityUpdate, CapabilityDelete,
		CapabilityServices, CapabilityLogs, CapabilityNetworks, CapabilityVolumes,
	}
}

func HasCapability(values []Capability, target Capability) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
