package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/suma/suma/server/internal/database"
	"github.com/suma/suma/server/internal/deployment"
)

func TestReleasePlanHTTPAuthenticationOwnershipAndPrivateInputBoundary(t *testing.T) {
	h := newDeliveryCreationHarness(t)
	for _, name := range []string{"plan-owner", "other-owner"} {
		if response := h.request(http.MethodPost, "/api/v1/delivery-projects", deliveryCreationBody(name)); response.Code != 201 {
			t.Fatal("create project", response.Code)
		}
	}
	var owner database.DeliveryProject
	if err := h.db.Where("name = ?", "plan-owner").First(&owner).Error; err != nil {
		t.Fatal(err)
	}
	id := strings.Repeat("f", 32)
	view := deployment.ReleasePlanView{ID: id, Operation: "deploy", ExpiresAt: time.Now().Add(time.Minute), CommitSHA: strings.Repeat("a", 40), Nodes: []deployment.ReleaseNodeView{{NodeID: "local", NodeName: "Local"}}, ImagesPinned: true}
	preview, err := json.Marshal(view)
	if err != nil {
		t.Fatal(err)
	}
	row := database.ReleasePlan{ID: id, Kind: "batch", OwnerID: owner.ID, Version: "private-version", PayloadCiphertext: []byte("private-encrypted-input"), PreviewJSON: string(preview), PreparationTaskID: "prepare-task", ExpiresAt: view.ExpiresAt, CreatedAt: time.Now()}
	if err := h.db.Create(&row).Error; err != nil {
		t.Fatal(err)
	}
	base := "/api/v1/delivery-projects/plan-owner/release-plans"
	for _, path := range []string{base + "/" + id, base + "?task_id=prepare-task"} {
		response := h.request(http.MethodGet, path, nil)
		if response.Code != 200 || response.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("cannot read safe preview", response.Code)
		}
		if strings.Contains(response.Body.String(), "private-encrypted-input") || strings.Contains(response.Body.String(), "payload_ciphertext") || strings.Contains(response.Body.String(), "private-version") {
			t.Fatal("private plan input exposed")
		}
	}
	if response := h.request(http.MethodGet, "/api/v1/delivery-projects/other-owner/release-plans/"+id, nil); response.Code != 404 {
		t.Fatal("plan crossed project boundary", response.Code)
	}
	if response := h.request(http.MethodPost, base, []byte(`{"operation":"arbitrary-command"}`)); response.Code != 422 {
		t.Fatal("unsupported operation accepted", response.Code)
	}
	if response := h.request(http.MethodPost, base+"/"+id+"/confirm", []byte(`{"node_ids":"all"}`)); response.Code != 400 {
		t.Fatal("invalid confirmation accepted", response.Code)
	}
	h.cookie = nil
	for _, request := range []struct {
		method, path string
		body         []byte
	}{{http.MethodGet, base + "/" + id, nil}, {http.MethodPost, base, []byte(`{}`)}, {http.MethodPost, base + "/" + id + "/confirm", []byte(`{}`)}} {
		if response := h.request(request.method, request.path, request.body); response.Code != 401 {
			t.Fatal("unauthenticated release endpoint", response.Code)
		}
	}
}
