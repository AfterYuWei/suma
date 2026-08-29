package network

import (
	"context"
	"encoding/json"
	"testing"
)

type stubService struct {
	list     []Resource
	resource Resource
	request  CreateRequest
	removeID string
	err      error
}

func (s *stubService) ListNetworks(context.Context) ([]Resource, error) { return s.list, s.err }

func (s *stubService) InspectNetwork(context.Context, string) (Resource, error) {
	return s.resource, s.err
}

func (s *stubService) CreateNetwork(_ context.Context, request CreateRequest) (Resource, error) {
	s.request = request
	return Resource{Name: request.Name, Driver: request.Driver}, s.err
}

func (s *stubService) RemoveNetwork(_ context.Context, id string) error {
	s.removeID = id
	return s.err
}

// The Service interface is the contract consumed by the API layer and
// implemented by the Docker adapter; keep it satisfied by a local stub.
var _ Service = (*stubService)(nil)

func TestCreateRequestJSONContract(t *testing.T) {
	data, err := json.Marshal(CreateRequest{Name: "webnet", Driver: "bridge", Subnet: "172.30.0.0/16", Gateway: "172.30.0.1", IPv6: true})
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]any{
		"name": "webnet", "driver": "bridge", "subnet": "172.30.0.0/16", "gateway": "172.30.0.1",
	} {
		got, ok := decoded[key]
		if !ok || got != want {
			t.Fatalf("expected field %q to serialize as %v, got %v (%s)", key, want, got, data)
		}
	}
}

func TestResourceIPAMSerialization(t *testing.T) {
	row := Resource{
		ID:         "net-1",
		Name:       "bridge0",
		Driver:     "bridge",
		Scope:      "local",
		IPv6:       true,
		Internal:   true,
		Containers: 2,
		AttachedContainers: []AttachedContainer{
			{ID: "container-1", Name: "api", IPv4Address: "10.10.0.2/24", IPv6Address: "fd00::2/64"},
		},
		Labels: map[string]string{"stack": "edge"},
		IPAM:   []IPAM{{Subnet: "10.10.0.0/24", Gateway: "10.10.0.1"}},
	}
	data, err := json.Marshal(row)
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Name               string              `json:"name"`
		Scope              string              `json:"scope"`
		Containers         int                 `json:"containers"`
		AttachedContainers []AttachedContainer `json:"attached_containers"`
		IPAM               []struct {
			Subnet  string `json:"subnet"`
			Gateway string `json:"gateway"`
		} `json:"ipam"`
	}
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Name != "bridge0" || decoded.Scope != "local" || decoded.Containers != 2 {
		t.Fatalf("unexpected resource serialization: %s", data)
	}
	if len(decoded.AttachedContainers) != 1 || decoded.AttachedContainers[0].Name != "api" || decoded.AttachedContainers[0].IPv4Address != "10.10.0.2/24" {
		t.Fatalf("unexpected attached container serialization: %s", data)
	}
	if len(decoded.IPAM) != 1 || decoded.IPAM[0].Subnet != "10.10.0.0/24" || decoded.IPAM[0].Gateway != "10.10.0.1" {
		t.Fatalf("unexpected IPAM serialization: %s", data)
	}
}

func TestInterfacePassesRequestsThrough(t *testing.T) {
	ctx := context.Background()
	service := &stubService{
		list:     []Resource{{Name: "edge-net", Driver: "overlay"}},
		resource: Resource{Name: "bridge0", IPAM: []IPAM{{Subnet: "fd00::/64"}}},
	}

	rows, err := service.ListNetworks(ctx)
	if err != nil || len(rows) != 1 || rows[0].Name != "edge-net" || rows[0].Driver != "overlay" {
		t.Fatalf("ListNetworks should pass through results: %v %v", rows, err)
	}
	detail, err := service.InspectNetwork(ctx, "net-1")
	if err != nil || detail.Name != "bridge0" || len(detail.IPAM) != 1 {
		t.Fatalf("InspectNetwork should pass through the resource: %+v %v", detail, err)
	}
	created, err := service.CreateNetwork(ctx, CreateRequest{Name: "mesh", Driver: "overlay"})
	if err != nil || service.request.Name != "mesh" || created.Driver != "overlay" {
		t.Fatalf("CreateNetwork should forward the request and echo a resource: %+v %v", service.request, err)
	}
	if err := service.RemoveNetwork(ctx, "net-9"); err != nil || service.removeID != "net-9" {
		t.Fatalf("RemoveNetwork should forward the identifier: %v", err)
	}
}
