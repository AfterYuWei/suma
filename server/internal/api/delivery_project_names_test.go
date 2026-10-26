package api

import (
	"context"
	"reflect"
	"testing"

	"github.com/suma/suma/server/internal/cd"
	"github.com/suma/suma/server/internal/database"
	projectdomain "github.com/suma/suma/server/internal/project"
)

func TestDeliveryProjectDisplayNamePreservesNodeAndNativeIdentity(t *testing.T) {
	h := newDeliveryCreationHarness(t)
	project := database.DeliveryProject{Name: "eizhu_admin", DeploymentName: "suma-cd-99"}
	if err := h.db.Create(&project).Error; err != nil {
		t.Fatal(err)
	}
	if err := h.db.Create(&database.DeliveryProjectNode{ProjectID: project.ID, NodeID: "local"}).Error; err != nil {
		t.Fatal(err)
	}
	deps := Dependencies{CD: cd.NewService(h.db, nil, nil, nil, nil, nil, nil)}
	for _, nodeID := range []string{"local", "other"} {
		row := projectdomain.ComposeSummary(nodeID, "suma-cd-99", "external", "stopped", false)
		before := row
		if err := annotateDeliveryProjectNames(context.Background(), deps, nodeID, &row); err != nil {
			t.Fatal(err)
		}
		if nodeID == "local" && row.DeliveryProjectName != "eizhu_admin" {
			t.Fatalf("missing CD display name: %#v", row)
		}
		if nodeID == "other" && row.DeliveryProjectName != "" {
			t.Fatalf("cross-node CD alias: %#v", row)
		}
		row.DeliveryProjectName = ""
		if !reflect.DeepEqual(row, before) {
			t.Fatal("display mapping changed native identity, capabilities or runtime state")
		}
	}
	managed := projectdomain.ComposeSummary("local", "suma-cd-99", "managed", "running", true)
	if err := annotateDeliveryProjectNames(context.Background(), deps, "local", &managed); err != nil || managed.DeliveryProjectName != "" {
		t.Fatal("a managed project was relabelled as CD")
	}
}
