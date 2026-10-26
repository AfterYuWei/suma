package api

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/suma/suma/server/internal/database"
)

func TestComposeUpdateUsesAuthorizedRegistryWithoutExposingSecrets(t *testing.T) {
	root := t.TempDir()
	secret := "fixture-cnb-private-value"
	auth := base64.StdEncoding.EncodeToString([]byte("cnb:" + secret))
	quote := func(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'" }
	command := filepath.Join(root, "compose-fixture")
	script := fmt.Sprintf(`#!/bin/sh
set -eu
umask 077
case " $* " in
 *" config "*) printf '%%s\n' '{"services":{"app":{"image":"docker.cnb.cool/yuweinfo/eizhu-admin:latest"}}}' ;;
 *" pull "*|*" up "*)
  test -n "${DOCKER_CONFIG:-}"
  case " $* " in *" pull "*) action=pull;; *) action=up;; esac
  cp "$DOCKER_CONFIG/config.json" %s/"$action.json"
  printf '%%s\n' "$DOCKER_CONFIG" >> %s
  printf 'daemon echoed %%s and %%s\n' %s %s
 ;;
esac
`, quote(root), quote(filepath.Join(root, "config-paths")), quote(secret), quote(auth))
	if err := os.WriteFile(command, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	h := newProjectHTTPHarnessWithCommand(t, command)
	for _, body := range []string{
		`{"name":"cnb","server_address":"docker.cnb.cool","auth_type":"basic","username":"cnb","secret":"` + secret + `","authorized_node_ids":["local"]}`,
		`{"name":"eizhu","backend":"compose","compose":"services:\n  app:\n    image: ${IMAGE_REF}\n","environment":"IMAGE_REF=docker.cnb.cool/yuweinfo/eizhu-admin:latest\n"}`,
	} {
		path := "/api/v1/nodes/local/projects"
		if strings.Contains(body, `"auth_type"`) {
			path = "/api/v1/credentials/registries"
		}
		response := h.request(http.MethodPost, path, []byte(body))
		if response.Code != http.StatusCreated {
			t.Fatalf("setup %s returned %d", path, response.Code)
		}
	}
	response := h.request(http.MethodPost, "/api/v1/nodes/local/projects/compose/eizhu/actions/update", []byte(`{}`))
	if response.Code != http.StatusAccepted {
		t.Fatalf("update status %d", response.Code)
	}
	var accepted struct {
		Data database.Task `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &accepted); err != nil {
		t.Fatal(err)
	}
	var row database.Task
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if err := h.db.First(&row, "id = ?", accepted.Data.ID).Error; err != nil {
			t.Fatal(err)
		}
		if row.Status == "success" || row.Status == "failed" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if row.Status != "success" {
		t.Fatalf("registry update failed: %s", row.Status)
	}
	for _, action := range []string{"pull", "up"} {
		raw, err := os.ReadFile(filepath.Join(root, action+".json"))
		if err != nil {
			t.Fatal(err)
		}
		var config struct {
			Auths map[string]map[string]string `json:"auths"`
		}
		if err := json.Unmarshal(raw, &config); err != nil {
			t.Fatal(err)
		}
		if len(config.Auths) != 1 || config.Auths["docker.cnb.cool"]["auth"] != auth {
			t.Fatal("authorized registry material did not reach Compose")
		}
	}
	paths, err := os.ReadFile(filepath.Join(root, "config-paths"))
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range strings.Fields(string(paths)) {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatal("private temporary registry file was retained", err)
		}
	}
	var logs []database.TaskLog
	if err := h.db.Where("task_id = ?", row.ID).Find(&logs).Error; err != nil {
		t.Fatal(err)
	}
	all := row.Message
	for _, entry := range logs {
		all += "\n" + entry.Message
	}
	if strings.Contains(all, secret) || strings.Contains(all, auth) {
		t.Fatal("registry credentials entered persisted task diagnostics")
	}
	if !strings.Contains(all, "[REDACTED]") {
		t.Fatal("controlled credential echo did not exercise redaction")
	}
}
