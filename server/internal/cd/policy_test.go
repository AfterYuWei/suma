package cd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateDeploymentPolicyAcceptsHostBindSources(t *testing.T) {
	outside := filepath.Join(t.TempDir(), "application.yml")
	if err := os.WriteFile(outside, []byte("server: {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// A path with a different meaning in the control plane must also be left
	// to the target Engine. Never resolve the control plane's symlinks here.
	localSymlink := filepath.Join(t.TempDir(), "application.yml")
	if err := os.Symlink("/etc/passwd", localSymlink); err != nil {
		t.Fatal(err)
	}
	for _, source := range []string{
		"/Data/Docker/DockerData/eizhu/application.yml",
		filepath.Join(t.TempDir(), "missing.yml"),
		outside,
		localSymlink,
	} {
		t.Run(source, func(t *testing.T) {
			document := renderedBind(source, "/app/application.yml", true)
			if err := validateDeploymentPolicy(document); err != nil {
				t.Fatalf("target-host bind source was checked against the control plane: %v", err)
			}
		})
	}
}

func renderedBind(source, target string, readOnly bool) string {
	encoded, _ := json.Marshal(map[string]any{"services": map[string]any{"app": map[string]any{
		"image": "example/app:1", "volumes": []any{map[string]any{
			"type": "bind", "source": source, "target": target, "read_only": readOnly,
		}},
	}}})
	return string(encoded)
}

func TestValidateDeploymentPolicyRejectsUnsafeHostBinds(t *testing.T) {
	tests := []struct {
		name, source, target, want string
		readOnly                   bool
	}{
		{"writable", "/srv/app/data", "/data", "must be read-only", false},
		{"empty", "", "/config", "absolute path", true},
		{"relative", "./application.yml", "/config", "absolute path", true},
		{"interpolated", "/srv/$APP/config", "/config", "non-interpolated", true},
		{"braced interpolation", "/srv/${APP}/config", "/config", "non-interpolated", true},
		{"root", "/", "/host", "protected host path", true},
		{"etc", "/etc/passwd", "/config", "protected host path", true},
		{"proc", "/proc/1/root", "/host", "protected host path", true},
		{"sys", "/sys", "/host", "protected host path", true},
		{"dev", "/dev/sda", "/host", "protected host path", true},
		{"run", "/run/credentials", "/host", "protected host path", true},
		{"var run", "/var/run", "/host", "protected host path", true},
		{"traversal", "/srv/app/../../etc/passwd", "/config", "protected host path", true},
		{"socket source", "/var/run/docker.sock", "/socket", "Docker socket", true},
		{"socket target", "/srv/app/socket", "/var/run/docker.sock", "Docker socket", true},
		{"socket alias source", "/run/docker.sock", "/socket", "Docker socket", true},
		{"socket alias target", "/srv/app/socket", "/run/docker.sock", "Docker socket", true},
		{"normalized socket target", "/srv/app/socket", "/var/run/./docker.sock", "Docker socket", true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateDeploymentPolicy(renderedBind(test.source, test.target, test.readOnly))
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}
	for _, source := range []string{"/etc-app/config", "/run-app/config", "/var/runtime/config", "/srv/app/../config"} {
		if err := validateDeploymentPolicy(renderedBind(source, "/config", true)); err != nil {
			t.Fatalf("safe host path %q was rejected: %v", source, err)
		}
	}
}

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
			err := validateDeploymentPolicy(document)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("validateDeploymentPolicy() error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestValidateDeploymentPolicyAcceptsNamedVolumes(t *testing.T) {
	document := `{"services":{"app":{"image":"example/app:1","volumes":[{"type":"volume","source":"data","target":"/data"}]}}}`
	if err := validateDeploymentPolicy(document); err != nil {
		t.Fatalf("validateDeploymentPolicy(named volume): %v", err)
	}
}
