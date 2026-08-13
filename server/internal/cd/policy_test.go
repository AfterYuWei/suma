package cd

import (
	"strings"
	"testing"
)

func TestValidateDeploymentPolicyRejectsHighRiskRuntimeFeatures(t *testing.T) {
	tests := []struct {
		name     string
		fragment string
		want     string
	}{
		{"privileged", `"privileged":true`, "privileged"},
		{"host namespace", `"network_mode":"host"`, "host namespace"},
		{"devices", `"devices":[{"source_path":"/dev/kvm"}]`, "host devices"},
		{"volumes from", `"volumes_from":["container:root"]`, "inherit"},
		{"all capabilities", `"cap_add":["ALL"]`, "capability"},
		{"sys admin capability", `"cap_add":["SYS_ADMIN"]`, "capability"},
		{"unconfined", `"security_opt":["seccomp=unconfined"]`, "confinement"},
		{"host cgroup", `"cgroup":"host"`, "cgroup"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			document := `{"services":{"app":{"image":"example/app:1",` + test.fragment + `}}}`
			err := validateDeploymentPolicy(t.TempDir(), document)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("validateDeploymentPolicy() error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestValidateDeploymentPolicyAcceptsNamedVolumes(t *testing.T) {
	document := `{"services":{"app":{"image":"example/app:1","volumes":[{"type":"volume","source":"data","target":"/data"}]}}}`
	if err := validateDeploymentPolicy(t.TempDir(), document); err != nil {
		t.Fatalf("validateDeploymentPolicy(named volume): %v", err)
	}
}
