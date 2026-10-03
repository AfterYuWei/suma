package docker

import (
	"context"
	"fmt"
	"github.com/docker/docker/pkg/stdcopy"
	"github.com/suma/suma/server/internal/projectlogs"
	"net/http"
	"strings"
	"testing"
)

func TestStructuredProjectLogDecodingAndOptions(t *testing.T) {
	for _, tty := range []bool{false, true} {
		t.Run(fmt.Sprint(tty), func(t *testing.T) {
			stamp := "2026-10-03T01:00:00.123456Z"
			stub := newDockerStub(t, map[string]http.HandlerFunc{
				"/containers/source/json": func(w http.ResponseWriter, r *http.Request) {
					writeJSON(w, 200, map[string]any{"Id": "source", "Config": map[string]any{"Tty": tty}})
				},
				"/containers/source/logs": func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Query().Get("since") != "1790989200.123456000" || r.URL.Query().Get("until") != "1790992800.000000000" || r.URL.Query().Get("tail") != "200" || r.URL.Query().Get("timestamps") != "1" {
						t.Errorf("Docker query options missing: %v", r.URL.Query())
					}
					w.Header().Set("Content-Type", "application/vnd.docker.raw-stream")
					var stdout, stderr interface{ Write([]byte) (int, error) } = w, w
					if !tty {
						stdout = stdcopy.NewStdWriter(w, stdcopy.Stdout)
						stderr = stdcopy.NewStdWriter(w, stdcopy.Stderr)
					}
					_, _ = stdout.Write([]byte(stamp + " he"))
					_, _ = stdout.Write([]byte("llo\n" + stamp + " \n"))
					_, _ = stderr.Write([]byte(stamp + " error\n"))
					_, _ = stdout.Write([]byte(stamp + " " + strings.Repeat("x", projectlogs.MaxLineBytes+200) + "\n"))
				},
			})
			adapter, err := New("tcp" + strings.TrimPrefix(stub.server.URL, "http"))
			if err != nil {
				t.Fatal(err)
			}
			defer adapter.Close()
			rows := []projectlogs.Record{}
			err = adapter.ReadProjectLogs(context.Background(), projectlogs.Source{ContainerID: "source", Service: "web"}, projectlogs.Query{Tail: 200, Since: stamp, Until: "2026-10-03T02:00:00Z"}, func(r projectlogs.Record) error { rows = append(rows, r); return nil })
			if err != nil || len(rows) != 4 || rows[0].Text != "hello" || rows[1].Text != "" || !rows[3].Truncated || len(rows[3].Text) > projectlogs.MaxLineBytes {
				t.Fatalf("decoded logs: count=%d err=%v", len(rows), err)
			}
			if tty && rows[2].Stream != "tty" || !tty && rows[2].Stream != "stderr" {
				t.Fatal("lost output stream")
			}
			for _, r := range rows {
				if r.Time.IsZero() || r.Service != "web" {
					t.Fatal("missing metadata")
				}
			}
		})
	}
}
func TestProjectLogSourceExactLabelIsolation(t *testing.T) {
	stub := newDockerStub(t, map[string]http.HandlerFunc{"/containers/json": func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Query().Get("filters"), "com.docker.compose.project=shop") {
			t.Error("missing project filter")
		}
		writeJSON(w, 200, []map[string]any{{"Id": "one", "Names": []string{"/shop-web-1"}, "State": "exited", "Labels": map[string]string{"com.docker.compose.project": "shop", "com.docker.compose.service": "web", "com.docker.compose.container-number": "1"}}, {"Id": "foreign", "Labels": map[string]string{"com.docker.compose.project": "shop-extra"}}})
	}})
	adapter, err := New("tcp" + strings.TrimPrefix(stub.server.URL, "http"))
	if err != nil {
		t.Fatal(err)
	}
	defer adapter.Close()
	sources, err := adapter.ProjectLogSources(context.Background(), "shop")
	if err != nil || len(sources) != 1 || sources[0].ContainerID != "one" || sources[0].State != "exited" {
		t.Fatalf("label isolation: %+v %v", sources, err)
	}
}
