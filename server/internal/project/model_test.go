package project

import "testing"

func TestComposeProjectIdentityIncludesBackendAndEngineScope(t *testing.T) {
	value := ComposeSummary("node-a", "shop", "external", "running", false)
	if value.Backend != BackendCompose || value.Scope.Kind != ScopeEngine || value.Scope.ID != "node-a" || value.NativeName != "shop" {
		t.Fatalf("summary = %#v", value)
	}
	if !HasCapability(value.Capabilities, CapabilityTakeover) || !HasCapability(value.Capabilities, CapabilityCleanup) || HasCapability(value.Capabilities, CapabilityEdit) {
		t.Fatalf("external capabilities = %#v", value.Capabilities)
	}
	managed := ComposeSummary("node-a", "shop", "managed", "running", true)
	if !HasCapability(managed.Capabilities, CapabilityEdit) || HasCapability(managed.Capabilities, CapabilityTakeover) || HasCapability(managed.Capabilities, CapabilityCleanup) {
		t.Fatalf("managed capabilities = %#v", managed.Capabilities)
	}
}

func TestBackendSeparatesFutureSwarmIdentity(t *testing.T) {
	compose := ComposeRef("shared", "shop")
	swarm := Ref{Backend: BackendSwarm, Scope: Scope{Kind: ScopeSwarm, ID: "shared"}, NativeName: "shop"}
	if compose == swarm {
		t.Fatal("Compose Project and Docker Swarm Stack identities must not collide")
	}
}
