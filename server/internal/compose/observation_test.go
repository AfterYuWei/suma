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
