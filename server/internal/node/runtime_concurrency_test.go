package node

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/suma/suma/server/internal/database"
	"github.com/suma/suma/server/internal/secret"
	"github.com/suma/suma/server/internal/testutil"
)

func TestRuntimeColdConnectionsAreIndependentAndCancelable(t *testing.T) {
	db, err := testutil.Open(t)
	if err != nil {
		t.Fatal(err)
	}
	store, err := secret.Open(filepath.Join(t.TempDir(), "secret.key"))
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewService(db, store, "unix:///unused.sock")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Close() })
	started, release := make(chan struct{}, 1), make(chan struct{})
	version := regexp.MustCompile(`^/v[0-9]+\.[0-9]+`)
	handler := func(block bool) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if version.ReplaceAllString(r.URL.Path, "") == "/_ping" {
				w.Header().Set("Api-Version", "1.44")
				_, _ = w.Write([]byte("OK"))
				return
			}
			if block {
				started <- struct{}{}
				select {
				case <-release:
				case <-r.Context().Done():
					return
				}
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"ID": "runtime-test", "Name": "runtime-test"})
		})
	}
	slow, fast := httptest.NewServer(handler(true)), httptest.NewServer(handler(false))
	t.Cleanup(slow.Close)
	t.Cleanup(fast.Close)
	if err := db.Model(&database.Node{}).Where("id = ?", "local").Updates(map[string]any{"connection_type": "tcp", "tls_mode": "disabled", "endpoint": strings.Replace(slow.URL, "http://", "tcp://", 1)}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.Node{ID: "fast", Name: "Fast", ConnectionType: "tcp", Endpoint: strings.Replace(fast.URL, "http://", "tcp://", 1), TLSMode: "disabled", AllowedBindRootsJSON: "[]", Enabled: true}).Error; err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	defer close(release)
	slowDone := make(chan error, 1)
	go func() { _, err := service.Runtime(ctx, "local"); slowDone <- err }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("slow connection did not start")
	}
	fastDone := make(chan error, 1)
	go func() { _, err := service.Runtime(ctx, "fast"); fastDone <- err }()
	select {
	case err := <-fastDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("slow cold connection blocked a different node")
	}
	waitCtx, cancelWait := context.WithCancel(ctx)
	cancelWait()
	if _, err := service.Runtime(waitCtx, "local"); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled waiter = %v", err)
	}
	// Credential invalidation during network I/O must not publish a client
	// established with the superseded connection configuration.
	service.invalidate("local")
	release <- struct{}{}
	select {
	case err := <-slowDone:
		if err == nil || !strings.Contains(err.Error(), "connection changed") {
			t.Fatalf("invalidated cold connection = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("invalidated connection did not finish")
	}
}
