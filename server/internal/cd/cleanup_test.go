package cd

import (
	"context"
	"reflect"
	"sort"
	"testing"

	"github.com/suma/suma/server/internal/database"
)

func TestCleanupProtectsNodeActiveDirectRollbackAndPendingImages(t *testing.T) {
	h := newCDHarness(t, ModeManual)
	release := func(ref, status string, previous *uint) database.DeliveryRelease {
		r := database.DeliveryRelease{ProjectID: h.project.ID, CommitSHA: ref, Status: status, ImageReferences: `["` + ref + `"]`, PreviousReleaseID: previous}
		if err := h.db.Create(&r).Error; err != nil {
			t.Fatal(err)
		}
		return r
	}
	ancestor := release("app:ancestor", StatusSucceeded, nil)
	previous := release("app:rollback", StatusSucceeded, &ancestor.ID)
	active := release("app:active", StatusSucceeded, &previous.ID)
	pending := release("app:pending", StatusDeploying, &active.ID)
	foreign := release("app:foreign", StatusSucceeded, nil)
	for _, row := range []database.DeliveryTargetState{{ProjectID: h.project.ID, NodeID: "remote", ActiveReleaseID: &active.ID}, {ProjectID: h.project.ID, NodeID: "other", ActiveReleaseID: &foreign.ID}} {
		if err := h.db.Create(&row).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := h.db.Create(&database.DeliveryReleaseDeployment{ReleaseID: pending.ID, NodeID: "remote", NodeName: "Remote", Status: StatusDeploying, PreviousReleaseID: &active.ID}).Error; err != nil {
		t.Fatal(err)
	}
	refs, err := h.service.CleanupImageReferences(context.Background(), "remote")
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(refs)
	if !reflect.DeepEqual(refs, []string{"app:active", "app:pending", "app:rollback"}) {
		t.Fatalf("protection = %v", refs)
	}
	refs, err = h.service.CleanupImageReferences(context.Background(), "other")
	if err != nil || !reflect.DeepEqual(refs, []string{"app:foreign"}) {
		t.Fatalf("foreign protection = %v %v", refs, err)
	}
	if err := h.db.Model(&database.DeliveryRelease{}).Where("id = ?", active.ID).Update("image_references", "broken").Error; err != nil {
		t.Fatal(err)
	}
	if _, err = h.service.CleanupImageReferences(context.Background(), "remote"); err == nil {
		t.Fatal("invalid protection must fail closed")
	}
	if err := h.db.Delete(&database.DeliveryRelease{}, active.ID).Error; err != nil {
		t.Fatal(err)
	}
	if _, err = h.service.CleanupImageReferences(context.Background(), "remote"); err == nil {
		t.Fatal("missing release must fail closed")
	}
}
