package volume

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

type stubService struct {
	resource Resource
	request  CreateRequest
	usage    map[string][]string
	removed  []string
}

func (s *stubService) ListVolumes(context.Context) ([]Resource, error) {
	return []Resource{{Name: "data", UsedBy: s.usage["data"]}}, nil
}

func (s *stubService) InspectVolume(_ context.Context, id string) (Resource, error) {
	return Resource{Name: id, UsedBy: s.usage[id]}, nil
}

func (s *stubService) CreateVolume(_ context.Context, request CreateRequest) (Resource, error) {
	s.request = request
	return Resource{Name: request.Name, Driver: firstNonEmpty(request.Driver, "local")}, nil
}

func (s *stubService) RemoveVolume(_ context.Context, id string) error {
	if len(s.usage[id]) > 0 {
		return ErrInUse
	}
	s.removed = append(s.removed, id)
	return nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

// The Service interface is the contract consumed by the API layer and
// implemented by the Docker adapter; keep it satisfied by a local stub.
var _ Service = (*stubService)(nil)

func TestErrInUseIdentityAndMessage(t *testing.T) {
	if ErrInUse == nil || ErrInUse.Error() != "volume is in use" {
		t.Fatalf("unexpected sentinel: %v", ErrInUse)
	}
	wrapped := errors.Join(errors.New("contextual prefix"), ErrInUse)
	if !errors.Is(wrapped, ErrInUse) {
		t.Fatal("sentinel must survive wrapping for callers using errors.Is")
	}
}

func TestRemoveFlowDistinguishesInUseVolumes(t *testing.T) {
	ctx := context.Background()
	service := &stubService{usage: map[string][]string{"busy-data": {"web", "api"}}}

	// The API layer compares identity with `err == volume.ErrInUse`; keep that valid.
	err := service.RemoveVolume(ctx, "busy-data")
	if err == nil || err != ErrInUse || !errors.Is(err, ErrInUse) {
		t.Fatalf("expected ErrInUse identity for a used volume, got %v", err)
	}
	if len(service.removed) != 0 {
		t.Fatal("a used volume must not be removed")
	}

	if err := service.RemoveVolume(ctx, "free-data"); err != nil {
		t.Fatalf("expected removal of an unused volume to succeed: %v", err)
	}
	if len(service.removed) != 1 || service.removed[0] != "free-data" {
		t.Fatalf("unexpected removed identifiers: %v", service.removed)
	}
}

func TestCreateRequestFieldsMapToDriverOptions(t *testing.T) {
	ctx := context.Background()
	service := &stubService{}
	row, err := service.CreateVolume(ctx, CreateRequest{
		Name:    "logs",
		Driver:  "nfs",
		Labels:  map[string]string{"team": "sre"},
		Options: map[string]string{"type": "nfs", "o": "addr=10.0.0.5,rw"},
	})
	if err != nil || service.request.Name != "logs" || service.request.Driver != "nfs" ||
		service.request.Options["o"] != "addr=10.0.0.5,rw" || service.request.Labels["team"] != "sre" {
		t.Fatalf("create options must pass through unchanged: %+v %v", service.request, err)
	}
	if row.Name != "logs" || row.Driver != "nfs" {
		t.Fatalf("unexpected created resource: %+v", row)
	}
}

func TestResourceEncodesUsageWarnings(t *testing.T) {
	row := Resource{
		Name:       "busy-data",
		Driver:     "local",
		Mountpoint: "/var/lib/docker/volumes/busy-data/_data",
		CreatedAt:  "2026-08-01T09:30:00Z",
		Scope:      "local",
		UsedBy:     []string{"web"},
		Size:       4096,
	}
	data, err := json.Marshal(row)
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Name   string   `json:"name"`
		UsedBy []string `json:"used_by"`
		Size   int64    `json:"size"`
	}
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Name != "busy-data" || decoded.Size != 4096 || len(decoded.UsedBy) != 1 || decoded.UsedBy[0] != "web" {
		t.Fatalf("used-by warning data must serialize for the API: %s", data)
	}
}
