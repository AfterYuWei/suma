package compose

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateRemoteBindMounts(t *testing.T) {
	tests := []struct { name, compose string; roots []string; want string }{
		{"allowed", "services:\n  app:\n    volumes:\n      - /srv/apps/data:/data:ro\n", []string{"/srv/apps"}, ""},
		{"relative", "services:\n  app:\n    volumes:\n      - ./data:/data\n", []string{"/srv/apps"}, "must be absolute"},
		{"outside", "services:\n  app:\n    volumes:\n      - /opt/data:/data\n", []string{"/srv/apps"}, "outside"},
		{"socket target", "services:\n  app:\n    volumes:\n      - /srv/apps/fake.sock:/var/run/docker.sock\n", []string{"/srv/apps"}, "Docker socket"},
	}
	for _, test := range tests { t.Run(test.name, func(t *testing.T) { err := ValidateRemoteBindMounts(test.compose, test.roots); if test.want == "" && err != nil { t.Fatal(err) }; if test.want != "" && (err == nil || !strings.Contains(err.Error(), test.want)) { t.Fatalf("expected %q, got %v", test.want, err) } }) }
}

func TestTargetEnvironmentUsesPrivateTemporaryFilesAndCleansUp(t *testing.T) {
	runner, err := NewRunner("docker compose")
	if err != nil { t.Fatal(err) }
	runner = runner.ForTarget(Target{Host: "tcp://docker.example:2376", TLSRequired: true, CA: "CA-SECRET", Certificate: "CERT-SECRET", PrivateKey: "KEY-SECRET", DockerConfig: `{"auths":{"registry.example":{}}}`})
	environment, cleanup, err := runner.commandEnvironment()
	if err != nil { t.Fatal(err) }
	joined := strings.Join(environment, "\n")
	if strings.Contains(joined, "SECRET") { t.Fatal("secret material leaked into environment") }
	var certPath, configPath string
	for _, value := range environment { if strings.HasPrefix(value, "DOCKER_CERT_PATH=") { certPath = strings.TrimPrefix(value, "DOCKER_CERT_PATH=") }; if strings.HasPrefix(value, "DOCKER_CONFIG=") { configPath = strings.TrimPrefix(value, "DOCKER_CONFIG=") } }
	if certPath == "" || configPath == "" { t.Fatalf("missing target environment: %v", environment) }
	if mode := mustMode(t, certPath); mode.Perm() != 0o700 { t.Fatalf("certificate directory mode is %o", mode.Perm()) }
	for _, name := range []string{"ca.pem", "cert.pem", "key.pem"} { if mode := mustMode(t, filepath.Join(certPath, name)); mode.Perm() != 0o600 { t.Fatalf("%s mode is %o", name, mode.Perm()) } }
	if mode := mustMode(t, filepath.Join(configPath, "config.json")); mode.Perm() != 0o600 { t.Fatalf("config mode is %o", mode.Perm()) }
	cleanup()
	if _, err := os.Stat(certPath); !os.IsNotExist(err) { t.Fatalf("temporary directory was not removed: %v", err) }
}

func mustMode(t *testing.T, path string) os.FileMode { t.Helper(); info, err := os.Stat(path); if err != nil { t.Fatal(err) }; return info.Mode() }
