package compose

import (
	"testing"
	"time"
)

func TestObserveRuntimeProjectKeepsScaleInstancesInOneService(t *testing.T) {
	config := RuntimeConfig{Image: "nginx:alpine", Environment: []string{"MODE=prod"}}
	snapshot := RuntimeProjectSnapshot{ProjectName: "shop", Containers: []RuntimeContainer{
		{ID: "one", Name: "shop-web-1", Service: "web", ContainerNumber: 1, CreatedAt: time.Unix(1, 0), Config: config},
		{ID: "two", Name: "shop-web-2", Service: "web", ContainerNumber: 2, CreatedAt: time.Unix(2, 0), Config: config},
		{ID: "three", Name: "shop-web-run", Service: "web", OneOff: true, CreatedAt: time.Unix(3, 0), Config: config},
	}}
	value := ObserveRuntimeProject(snapshot)
	if len(value.Services) != 1 || value.Services[0].Name != "web" || value.Services[0].DesiredReplicas != 2 || len(value.Services[0].Instances) != 2 {
		t.Fatalf("observation = %#v", value)
	}
	if len(value.OneOffContainers) != 1 || value.OneOffContainers[0].ContainerID != "three" {
		t.Fatalf("one-off containers = %#v", value.OneOffContainers)
	}
}

func TestObserveRuntimeProjectUsesEmptyCollectionsInsteadOfNil(t *testing.T) {
	value := ObserveRuntimeProject(RuntimeProjectSnapshot{ProjectName: "shop", Containers: []RuntimeContainer{{ID: "run", Service: "job", OneOff: true, Config: RuntimeConfig{Image: "job:v1"}}}})
	if value.Services == nil || value.Networks == nil || value.Volumes == nil || value.OneOffContainers == nil || value.OrphanContainers == nil || value.Warnings == nil {
		t.Fatalf("observation contains nil collections: %#v", value)
	}
}

func TestObserveRuntimeServiceUsesMajorityAndReportsDrift(t *testing.T) {
	stable := RuntimeConfig{Image: "app:v1", Command: []string{"serve"}}
	drifted := RuntimeConfig{Image: "app:v2", Command: []string{"serve"}}
	value := ObserveRuntimeProject(RuntimeProjectSnapshot{ProjectName: "shop", Containers: []RuntimeContainer{
		{ID: "one", Service: "web", ContainerNumber: 1, CreatedAt: time.Unix(1, 0), Config: stable},
		{ID: "two", Service: "web", ContainerNumber: 2, CreatedAt: time.Unix(2, 0), Config: stable},
		{ID: "three", Service: "web", ContainerNumber: 3, CreatedAt: time.Unix(3, 0), Config: drifted},
	}})
	service := value.Services[0]
	if service.DriftStatus != "runtime_drift" || service.DesiredReplicas != 2 || len(service.ConfigVariants) != 2 {
		t.Fatalf("service = %#v", service)
	}
	if service.ConfigVariants[0].Config.Image != "app:v1" {
		t.Fatalf("canonical variant = %#v", service.ConfigVariants[0])
	}
	if len(service.DriftFields) != 1 || service.DriftFields[0] != "image" || len(service.ConfigVariants[1].DifferenceFields) != 1 || service.ConfigVariants[1].DifferenceFields[0] != "image" {
		t.Fatalf("drift fields = %#v / %#v", service.DriftFields, service.ConfigVariants)
	}
	if len(service.DriftReasons) != 1 || service.DriftReasons[0] != "runtime_drift" {
		t.Fatalf("drift reasons = %#v", service.DriftReasons)
	}
}

func TestObserveRuntimeServiceExplainsEvidenceBasedDrift(t *testing.T) {
	manual := ObserveRuntimeProject(RuntimeProjectSnapshot{ProjectName: "shop", Containers: []RuntimeContainer{
		{ID: "one", Service: "web", ConfigHash: "same", Config: RuntimeConfig{Image: "app:v1", Resources: RuntimeResources{Memory: 128}}},
		{ID: "two", Service: "web", ConfigHash: "same", Config: RuntimeConfig{Image: "app:v1", Resources: RuntimeResources{Memory: 256}}},
	}}).Services[0]
	if len(manual.DriftReasons) != 1 || manual.DriftReasons[0] != "manual_modification" || len(manual.DriftFields) != 1 || manual.DriftFields[0] != "resources" {
		t.Fatalf("manual drift = %#v", manual)
	}

	partial := ObserveRuntimeProject(RuntimeProjectSnapshot{ProjectName: "shop", Containers: []RuntimeContainer{
		{ID: "one", Service: "web", ConfigHash: "old", Config: RuntimeConfig{Image: "app:v1"}},
		{ID: "two", Service: "web", ConfigHash: "new", Config: RuntimeConfig{Image: "app:v2"}},
	}}).Services[0]
	if len(partial.DriftReasons) != 1 || partial.DriftReasons[0] != "partial_recreate" {
		t.Fatalf("partial recreate = %#v", partial)
	}
}

func TestObserveRuntimeServiceTieUsesNewestVariant(t *testing.T) {
	value := ObserveRuntimeProject(RuntimeProjectSnapshot{ProjectName: "shop", Containers: []RuntimeContainer{
		{ID: "old", Service: "web", CreatedAt: time.Unix(1, 0), Config: RuntimeConfig{Image: "app:old"}},
		{ID: "new", Service: "web", CreatedAt: time.Unix(2, 0), Config: RuntimeConfig{Image: "app:new"}},
	}})
	if value.Services[0].ConfigVariants[0].Config.Image != "app:new" {
		t.Fatalf("variants = %#v", value.Services[0].ConfigVariants)
	}
}
