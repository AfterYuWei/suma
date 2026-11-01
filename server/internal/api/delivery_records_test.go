package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/suma/suma/server/internal/database"
)

func TestDeliveryRecordHTTPPaginationAliasesAndSnapshotBoundaries(t *testing.T) {
	h := newDeliveryCreationHarness(t)
	if response := h.request(http.MethodPost, "/api/v1/delivery-projects", deliveryCreationBody("records")); response.Code != http.StatusCreated {
		t.Fatal("project creation", response.Code)
	}
	var project database.DeliveryProject
	if err := h.db.Where("name = ?", "records").First(&project).Error; err != nil {
		t.Fatal(err)
	}
	release := database.DeliveryRelease{ProjectID: project.ID, RepositoryURL: project.GitCloneURL, CommitSHA: "record-commit", Status: "rejected", TriggerType: "manual", ComposeFilesJSON: `["compose.yml"]`}
	if err := h.db.Create(&release).Error; err != nil {
		t.Fatal(err)
	}
	alias := database.DeliveryRelease{ProjectID: project.ID, RepositoryURL: project.GitCloneURL, CommitSHA: release.CommitSHA, CanonicalReleaseID: &release.ID, Status: "failed", TriggerType: "manual"}
	if err := h.db.Create(&alias).Error; err != nil {
		t.Fatal(err)
	}
	record := database.DeliveryRecord{ProjectID: project.ID, ReleaseID: release.ID, Operation: "deploy", Status: "failed", SnapshotComplete: true, ActorName: "operator", ComposeFilesJSON: `["compose.yml"]`, NodeIDsJSON: `["local"]`, RegistryCredentialIDsJSON: `[]`, EnvironmentCiphertext: project.EnvironmentCiphertext, DeploymentTimeout: 60, ConfigHash: "retained-hash"}
	if err := h.db.Create(&record).Error; err != nil {
		t.Fatal(err)
	}
	base := "/api/v1/delivery-projects/records"
	for _, path := range []string{fmt.Sprintf("%s/records?release_id=%d&page=1&page_size=10", base, alias.ID), fmt.Sprintf("%s/records?operation=deploy&status=failed", base)} {
		response := h.request(http.MethodGet, path, nil)
		var result struct {
			Data struct {
				Items  []database.DeliveryRecord `json:"items"`
				Total  int64                     `json:"total"`
				Counts map[string]int64          `json:"counts"`
			} `json:"data"`
		}
		if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &result) != nil || result.Data.Total != 1 || result.Data.Counts["deploy"] != 1 || len(result.Data.Items) != 1 {
			t.Fatal("record filters", response.Code)
		}
		if strings.Contains(response.Body.String(), "test-only-manual-environment") || strings.Contains(response.Body.String(), "environment_ciphertext") {
			t.Fatal("record list exposed environment")
		}
	}
	response := h.request(http.MethodGet, fmt.Sprintf("%s/releases/%d", base, alias.ID), nil)
	var canonical struct {
		Data database.DeliveryRelease `json:"data"`
	}
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &canonical) != nil || canonical.Data.ID != release.ID {
		t.Fatal("old release alias is inaccessible")
	}
	response = h.request(http.MethodGet, fmt.Sprintf("%s/records/%d/environment", base, record.ID), nil)
	if response.Code != 200 || response.Header().Get("Cache-Control") != "no-store" || !strings.Contains(response.Body.String(), "test-only-manual-environment") {
		t.Fatal("explicit snapshot environment read failed")
	}
	var count int64
	h.db.Model(&database.AuditLog{}).Where("action = ?", "cd.record.environment.read").Count(&count)
	if count != 1 {
		t.Fatal("snapshot value read was not audited")
	}
	for _, query := range []string{"page=0", "page_size=101", "release_id=invalid"} {
		if response := h.request(http.MethodGet, base+"/records?"+query, nil); response.Code != 400 {
			t.Fatal("invalid record query accepted", query)
		}
	}
	path := fmt.Sprintf("%s/releases/%d", base, release.ID)
	if response := h.request(http.MethodPost, path+"/approve", nil); response.Code != http.StatusNotFound {
		t.Fatal("separate approval endpoint is still registered", response.Code)
	}
	if response := h.request(http.MethodPost, path+"/deploy", nil); response.Code != http.StatusConflict {
		t.Fatal("rejected release was published")
	}
	for _, body := range []string{`{}`, `{"record_id":0}`, `{"record_id":"invalid"}`} {
		if response := h.request(http.MethodPost, path+"/rollback", []byte(body)); response.Code != 400 {
			t.Fatal("rollback without record accepted")
		}
	}
	if response := h.request(http.MethodPost, path+"/rollback", []byte(fmt.Sprintf(`{"record_id":%d,"node_ids":[]}`, record.ID))); response.Code != 409 {
		t.Fatal("empty temporary targets accepted")
	}
	if err := h.db.Model(&record).Update("snapshot_complete", false).Error; err != nil {
		t.Fatal(err)
	}
	if response := h.request(http.MethodPost, path+"/rollback", []byte(fmt.Sprintf(`{"record_id":%d}`, record.ID))); response.Code != 409 {
		t.Fatal("incomplete rollback snapshot accepted")
	}
	if response := h.request(http.MethodPost, path+"/remediations/retry-failed", []byte(`{}`)); response.Code != 400 {
		t.Fatal("retry without source accepted")
	}
	h.cookie = nil
	if response := h.request(http.MethodGet, base+"/records", nil); response.Code != 401 {
		t.Fatal("record history accessible without session")
	}
	if response := h.request(http.MethodGet, fmt.Sprintf("%s/records/%d/environment", base, record.ID), nil); response.Code != 401 {
		t.Fatal("snapshot values accessible without session")
	}
}
