package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/suma/suma/server/internal/cd"
	"github.com/suma/suma/server/internal/database"
)

func TestDeliveryHistoryHTTPIncludesNodeProjectsWithoutBatchProjects(t *testing.T) {
	h := newDeliveryCreationHarness(t)
	project := database.ProjectCD{NodeID: "local", ProjectName: "deployed-app", WebhookID: "history"}
	if err := h.db.Create(&project).Error; err != nil {
		t.Fatal(err)
	}
	if err := h.db.Create(&database.ProjectCDRecord{ProjectCDID: project.ID, Operation: "apply", Status: "succeeded", Actor: "operator", TaskID: "deployed-task", CommitSHA: "deployed-commit", Revision: 1, SnapshotCiphertext: []byte("private-snapshot")}).Error; err != nil {
		t.Fatal(err)
	}
	response := h.request(http.MethodGet, "/api/v1/delivery-records?source=project_cd&operation=deploy", nil)
	var result struct{ Data cd.HistoryPage }
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &result) != nil || result.Data.Total != 1 || len(result.Data.Items) != 1 {
		t.Fatal("existing deployment was not included", response.Code, response.Body.String())
	}
	row := result.Data.Items[0]
	if row.ProjectName != "deployed-app" || row.NodeID != "local" || row.TaskID != "deployed-task" || row.ReleaseID != nil || row.Operation != "deploy" {
		t.Fatal("source identity was changed", row)
	}
	if strings.Contains(response.Body.String(), "private-snapshot") || strings.Contains(response.Body.String(), "ciphertext") {
		t.Fatal("history revealed private evidence")
	}
	for _, query := range []string{"page=0", "page=bad", "page_size=101", "source=unknown", "operation=unknown", "status=unknown"} {
		if response := h.request(http.MethodGet, "/api/v1/delivery-records?"+query, nil); response.Code != 400 {
			t.Fatal("invalid boundary accepted", query, response.Code)
		}
	}
	h.cookie = nil
	if response := h.request(http.MethodGet, "/api/v1/delivery-records", nil); response.Code != 401 {
		t.Fatal("unauthenticated history was returned", response.Code)
	}
}
