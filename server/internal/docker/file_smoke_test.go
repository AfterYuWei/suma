//go:build dockersmoke

package docker_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	dockercontainer "github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/api/types/volume"
	"github.com/docker/docker/client"
	"github.com/suma/suma/server/internal/api"
	"github.com/suma/suma/server/internal/audit"
	"github.com/suma/suma/server/internal/auth"
	"github.com/suma/suma/server/internal/containerfiles"
	"github.com/suma/suma/server/internal/database"
	"github.com/suma/suma/server/internal/docker"
	"github.com/suma/suma/server/internal/node"
	"github.com/suma/suma/server/internal/secret"
	"github.com/suma/suma/server/internal/task"
)

func TestContainerFileEditingRealDocker(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		t.Fatal(err)
	}
	defer cli.Close()
	if _, err := cli.Ping(ctx); err != nil {
		t.Skipf("Docker unavailable: %v", err)
	}
	volumeName := "suma-file-smoke-" + time.Now().Format("20060102150405")
	secondVolume := volumeName + "-other"
	root := t.TempDir()
	bindFile := filepath.Join(root, "mounted.yaml")
	if err := os.WriteFile(bindFile, []byte("bind: before\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := cli.VolumeCreate(ctx, volume.CreateOptions{Name: volumeName}); err != nil {
		t.Fatal(err)
	}
	defer cli.VolumeRemove(context.Background(), volumeName, true)
	if _, err := cli.VolumeCreate(ctx, volume.CreateOptions{Name: secondVolume}); err != nil {
		t.Fatal(err)
	}
	defer cli.VolumeRemove(context.Background(), secondVolume, true)
	created, err := cli.ContainerCreate(ctx, &dockercontainer.Config{Image: "alpine:3.24", Cmd: []string{"sleep", "120"}}, &dockercontainer.HostConfig{Mounts: []mount.Mount{{Type: mount.TypeVolume, Source: volumeName, Target: "/data"}, {Type: mount.TypeVolume, Source: secondVolume, Target: "/other"}, {Type: mount.TypeBind, Source: bindFile, Target: "/mounted.yaml"}}}, nil, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	defer cli.ContainerRemove(context.Background(), created.ID, dockercontainer.RemoveOptions{Force: true})
	if err := cli.ContainerStart(ctx, created.ID, dockercontainer.StartOptions{}); err != nil {
		t.Fatal(err)
	}
	adapter, err := docker.New(client.DefaultDockerHost)
	if err != nil {
		t.Fatal(err)
	}
	defer adapter.Close()
	db, err := database.Open(filepath.Join(root, "suma.db"))
	if err != nil {
		t.Fatal(err)
	}
	secrets, err := secret.Open(filepath.Join(root, "secret.key"))
	if err != nil {
		t.Fatal(err)
	}
	service := containerfiles.NewService(db, secrets)
	if err := service.Apply(ctx, adapter, created.ID, containerfiles.Action{Action: "create_file", Path: "/data/app.yaml"}); err != nil {
		t.Fatal(err)
	}
	listing, err := service.List(ctx, adapter, created.ID, "/data", "")
	if err != nil || len(listing.Entries) != 1 || listing.Entries[0].Name != "app.yaml" {
		t.Fatalf("list: %+v, %v", listing, err)
	}
	opened, err := service.Read(ctx, adapter, created.ID, "/data/app.yaml")
	if err != nil || !opened.Persistent || opened.ReadOnly {
		t.Fatalf("read: %+v, %v", opened, err)
	}
	saved, err := service.Save(ctx, adapter, "local", created.ID, opened.Path, opened.ETag, "name: smoke\n", nil)
	if err != nil || saved.Content != "name: smoke\n" {
		t.Fatalf("save: %+v, %v", saved, err)
	}
	history, err := service.History(ctx, adapter, "local", created.ID, opened.Path)
	if err != nil || len(history) != 2 {
		t.Fatalf("history: %+v, %v", history, err)
	}
	if _, err := service.Restore(ctx, adapter, "local", created.ID, opened.Path, history[1].ID, saved.ETag, nil); err != nil {
		t.Fatal(err)
	}
	layerPath := "/tmp/suma-layer-smoke.yaml"
	if err := service.Apply(ctx, adapter, created.ID, containerfiles.Action{Action: "create_file", Path: layerPath}); err != nil {
		t.Fatal(err)
	}
	layer, err := service.Read(ctx, adapter, created.ID, layerPath)
	if err != nil || layer.Persistent || layer.ReadOnly {
		t.Fatalf("writable container layer: %+v, %v", layer, err)
	}
	layer, err = service.Save(ctx, adapter, "local", created.ID, layerPath, layer.ETag, "layer: saved\n", nil)
	if err != nil || layer.Content != "layer: saved\n" || layer.Persistent || layer.ReadOnly {
		t.Fatalf("container layer save: %+v, %v", layer, err)
	}
	layerHistory, err := service.History(ctx, adapter, "local", created.ID, layerPath)
	if err != nil || len(layerHistory) != 2 {
		t.Fatalf("container layer history: %+v, %v", layerHistory, err)
	}
	if _, err := service.Restore(ctx, adapter, "local", created.ID, layerPath, layerHistory[1].ID, layer.ETag, nil); err != nil {
		t.Fatal(err)
	}
	readOnlyContainer, err := cli.ContainerCreate(ctx, &dockercontainer.Config{Image: "alpine:3.24", Cmd: []string{"sleep", "120"}}, &dockercontainer.HostConfig{ReadonlyRootfs: true}, nil, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	defer cli.ContainerRemove(context.Background(), readOnlyContainer.ID, dockercontainer.RemoveOptions{Force: true})
	if err := cli.ContainerStart(ctx, readOnlyContainer.ID, dockercontainer.StartOptions{}); err != nil {
		t.Fatal(err)
	}
	readOnlyFile, err := service.Read(ctx, adapter, readOnlyContainer.ID, "/etc/alpine-release")
	if err != nil || !readOnlyFile.ReadOnly {
		t.Fatalf("read-only container layer: %+v, %v", readOnlyFile, err)
	}
	if _, err := service.Save(ctx, adapter, "local", readOnlyContainer.ID, readOnlyFile.Path, readOnlyFile.ETag, "blocked", nil); err != containerfiles.ErrUnsupported {
		t.Fatalf("read-only container layer save: %v", err)
	}
	if err := service.Apply(ctx, adapter, readOnlyContainer.ID, containerfiles.Action{Action: "create_file", Path: "/tmp/blocked"}); err != containerfiles.ErrUnsupported {
		t.Fatalf("read-only container layer create: %v", err)
	}
	if err := service.Apply(ctx, adapter, created.ID, containerfiles.Action{Action: "copy", Path: opened.Path, Target: "/data/copy.yaml"}); err != nil {
		t.Fatal(err)
	}
	if err := service.Apply(ctx, adapter, created.ID, containerfiles.Action{Action: "copy", Path: opened.Path, Target: "/data/copy.yaml"}); err == nil {
		t.Fatal("copy unexpectedly overwrote existing target")
	}
	if err := service.Apply(ctx, adapter, created.ID, containerfiles.Action{Action: "move", Path: "/data/copy.yaml", Target: "/data/moved.yaml"}); err != nil {
		t.Fatal(err)
	}
	if err := service.Apply(ctx, adapter, created.ID, containerfiles.Action{Action: "move", Path: "/data/moved.yaml", Target: "/other/moved.yaml"}); err != nil {
		t.Fatal(err)
	}
	if err := service.Apply(ctx, adapter, created.ID, containerfiles.Action{Action: "delete", Path: "/other/moved.yaml"}); err != nil {
		t.Fatal(err)
	}
	bind, err := service.Read(ctx, adapter, created.ID, "/mounted.yaml")
	if err != nil || !bind.SingleFileBind {
		t.Fatalf("single-file bind: %+v, %v", bind, err)
	}
	if _, err := service.Save(ctx, adapter, "local", created.ID, bind.Path, bind.ETag, "bind: after\n", nil); err != nil {
		t.Fatal(err)
	}
	actual, err := os.ReadFile(bindFile)
	if err != nil || string(actual) != "bind: after\n" {
		t.Fatalf("bind save: %q, %v", actual, err)
	}
	nodes, err := node.NewService(db, secrets, client.DefaultDockerHost)
	if err != nil {
		t.Fatal(err)
	}
	defer nodes.Close()
	authService := auth.NewService(db, time.Hour, secrets)
	if _, err := authService.Initialize(ctx, "admin", "admin@example.test", "", "long-password"); err != nil {
		t.Fatal(err)
	}
	token, _, err := authService.Login(ctx, "admin", "long-password", "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	router := api.NewRouter(api.Dependencies{Engine: adapter, Containers: adapter, Files: service, Auth: authService, Audit: audit.NewService(db), Tasks: task.NewService(db), Nodes: nodes})
	request := func(method, url string, body []byte) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, url, bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(&http.Cookie{Name: "suma_session", Value: token})
		response := httptest.NewRecorder()
		router.ServeHTTP(response, req)
		return response
	}
	base := "/api/v1/nodes/local/containers/" + created.ID + "/files"
	response := request(http.MethodGet, base+"/content?path=/data/app.yaml", nil)
	if response.Code != http.StatusOK {
		t.Fatalf("HTTP read: %d %s", response.Code, response.Body.String())
	}
	var envelope struct {
		Data containerfiles.Content `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	badBody, _ := json.Marshal(map[string]string{"path": "/data/app.yaml", "content": "via API\n", "etag": "stale"})
	if response := request(http.MethodPut, base+"/content", badBody); response.Code != http.StatusConflict {
		t.Fatalf("HTTP conflict: %d %s", response.Code, response.Body.String())
	}
	goodBody, _ := json.Marshal(map[string]string{"path": "/data/app.yaml", "content": "via API\n", "etag": envelope.Data.ETag})
	if response := request(http.MethodPut, base+"/content", goodBody); response.Code != http.StatusOK {
		t.Fatalf("HTTP save: %d %s", response.Code, response.Body.String())
	}
	layerResponse := request(http.MethodGet, base+"/content?path="+layerPath, nil)
	if layerResponse.Code != http.StatusOK {
		t.Fatalf("HTTP container layer read: %d %s", layerResponse.Code, layerResponse.Body.String())
	}
	var layerEnvelope struct {
		Data containerfiles.Content `json:"data"`
	}
	if err := json.Unmarshal(layerResponse.Body.Bytes(), &layerEnvelope); err != nil {
		t.Fatal(err)
	}
	if layerEnvelope.Data.ReadOnly || layerEnvelope.Data.Persistent {
		t.Fatalf("HTTP container layer flags: %+v", layerEnvelope.Data)
	}
	layerBody, _ := json.Marshal(map[string]string{"path": layerPath, "content": "layer: via API\n", "etag": layerEnvelope.Data.ETag})
	if response := request(http.MethodPut, base+"/content", layerBody); response.Code != http.StatusOK {
		t.Fatalf("HTTP container layer save: %d %s", response.Code, response.Body.String())
	}
	if response := request(http.MethodGet, "/api/v1/nodes/missing/containers/"+created.ID+"/files?path=/", nil); response.Code != http.StatusNotFound {
		t.Fatalf("wrong node: %d %s", response.Code, response.Body.String())
	}
}
