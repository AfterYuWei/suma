package compose

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateRemoteBindMounts(t *testing.T) {
	tests := []struct{ name, compose, want string }{
		{"absolute", "services:\n  app:\n    volumes:\n      - /opt/data:/data:ro\n", ""},
		{"relative", "services:\n  app:\n    volumes:\n      - ./data:/data\n", "must be absolute"},
		{"socket target", "services:\n  app:\n    volumes:\n      - /srv/apps/fake.sock:/var/run/docker.sock\n", "Docker socket"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := ValidateRemoteBindMounts(test.compose)
			if test.want == "" && err != nil {
				t.Fatal(err)
			}
			if test.want != "" && (err == nil || !strings.Contains(err.Error(), test.want)) {
				t.Fatalf("expected %q, got %v", test.want, err)
			}
		})
	}
}

func TestValidateComposeBindMountsAllowsConfirmedDockerSocket(t *testing.T) {
	socket := "services:\n  app:\n    volumes:\n      - /var/run/docker.sock:/var/run/docker.sock\n"
	if err := ValidateComposeBindMounts(socket, false, false); err == nil || !strings.Contains(err.Error(), "explicit confirmation") {
		t.Fatalf("unconfirmed local socket error = %v", err)
	}
	if err := ValidateComposeBindMounts(socket, false, true); err != nil {
		t.Fatalf("confirmed local socket: %v", err)
	}
	if err := ValidateComposeBindMounts(socket, true, true); err != nil {
		t.Fatalf("confirmed remote socket: %v", err)
	}
	interpolated := "services:\n  app:\n    volumes:\n      - ${DOCKER_SOCKET}:/var/run/docker.sock\n"
	if err := ValidateComposeBindMounts(interpolated, true, true); err == nil || !strings.Contains(err.Error(), "cannot be interpolated") {
		t.Fatalf("confirmed remote interpolated socket error = %v", err)
	}
}

func TestTargetEnvironmentUsesPrivateTemporaryFilesAndCleansUp(t *testing.T) {
	runner, err := NewRunner("docker compose")
	if err != nil {
		t.Fatal(err)
	}
	runner = runner.ForTarget(Target{Host: "tcp://docker.example:2376", TLSRequired: true, CA: "CA-SECRET", Certificate: "CERT-SECRET", PrivateKey: "KEY-SECRET", DockerConfig: `{"auths":{"registry.example":{}}}`})
	environment, cleanup, err := runner.commandEnvironment()
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(environment, "\n")
	if strings.Contains(joined, "SECRET") {
		t.Fatal("secret material leaked into environment")
	}
	var certPath, configPath string
	for _, value := range environment {
		if strings.HasPrefix(value, "DOCKER_CERT_PATH=") {
			certPath = strings.TrimPrefix(value, "DOCKER_CERT_PATH=")
		}
		if strings.HasPrefix(value, "DOCKER_CONFIG=") {
			configPath = strings.TrimPrefix(value, "DOCKER_CONFIG=")
		}
	}
	if certPath == "" || configPath == "" {
		t.Fatalf("missing target environment: %v", environment)
	}
	if mode := mustMode(t, certPath); mode.Perm() != 0o700 {
		t.Fatalf("certificate directory mode is %o", mode.Perm())
	}
	for _, name := range []string{"ca.pem", "cert.pem", "key.pem"} {
		if mode := mustMode(t, filepath.Join(certPath, name)); mode.Perm() != 0o600 {
			t.Fatalf("%s mode is %o", name, mode.Perm())
		}
	}
	if mode := mustMode(t, filepath.Join(configPath, "config.json")); mode.Perm() != 0o600 {
		t.Fatalf("config mode is %o", mode.Perm())
	}
	cleanup()
	if _, err := os.Stat(certPath); !os.IsNotExist(err) {
		t.Fatalf("temporary directory was not removed: %v", err)
	}
}

func mustMode(t *testing.T, path string) os.FileMode {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info.Mode()
}

func TestRegistryOverridePreservesTargetAndUsesPrivateFiles(t *testing.T) {
	t.Setenv("DOCKER_HOST", "unix:///ambient.sock")
	t.Setenv("DOCKER_CONFIG", "/unrelated-config")
	for _, target := range []Target{{}, {Host: "unix:///explicit.sock"}, {Host: "tcp://docker.example:2376", TLSRequired: true, CA: "fixture-ca", Certificate: "fixture-cert", PrivateKey: "fixture-key"}, {Host: "unix:///agent-proxy.sock"}} {
		t.Run(target.Host, func(t *testing.T) {
			base, err := NewRunner("docker compose")
			if err != nil {
				t.Fatal(err)
			}
			if target.Host != "" {
				base = base.ForTarget(target)
			}
			runner := base.WithDockerConfig(`{"auths":{"docker.cnb.cool":{"auth":"fixture-auth"}}}`).(*CLIRunner)
			env, cleanup, err := runner.commandEnvironment()
			if err != nil {
				t.Fatal(err)
			}
			defer cleanup()
			values := map[string]string{}
			for _, line := range env {
				key, value, _ := strings.Cut(line, "=")
				values[key] = value
			}
			wantHost := target.Host
			if wantHost == "" {
				wantHost = "unix:///ambient.sock"
			}
			if values["DOCKER_HOST"] != wantHost || values["DOCKER_CONFIG"] == "/unrelated-config" {
				t.Fatal("registry auth changed the runtime or reused ambient credentials")
			}
			path := values["DOCKER_CONFIG"]
			if path == "" || mustMode(t, path).Perm() != 0o700 || mustMode(t, filepath.Join(path, "config.json")).Perm() != 0o600 {
				t.Fatal("registry material is not private")
			}
			if target.TLSRequired {
				if values["DOCKER_TLS_VERIFY"] != "1" || values["DOCKER_CERT_PATH"] == "" {
					t.Fatal("registry configuration dropped mutual TLS")
				}
				for file, want := range map[string]string{"ca.pem": target.CA, "cert.pem": target.Certificate, "key.pem": target.PrivateKey} {
					filePath := filepath.Join(values["DOCKER_CERT_PATH"], file)
					data, err := os.ReadFile(filePath)
					if err != nil || string(data) != want || mustMode(t, filePath).Perm() != 0o600 {
						t.Fatal("registry configuration changed TLS material or permissions", err)
					}
				}
			}
			cleanup()
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Fatal("registry material not cleaned", err)
			}
			if base.registryConfig != "" {
				t.Fatal("per-operation credentials mutated the shared runner")
			}
		})
	}
}
