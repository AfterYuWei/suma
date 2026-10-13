package docker

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"io"
	"log"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/docker/docker/client"
	domain "github.com/suma/suma/server/internal/container"
	monitor "github.com/suma/suma/server/internal/containermonitor"
)

func TestContainerObservationMutualTLS(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "isolated-docker"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")}}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(parsed)
	certificate := tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
	id := strings.Repeat("d", 64)
	canceled := make(chan struct{}, 1)
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if len(r.TLS.VerifiedChains) == 0 {
			t.Error("client certificate was not verified")
		}
		path := apiVersionPrefix.ReplaceAllString(r.URL.Path, "")
		switch path {
		case "/_ping":
			w.Header().Set("Api-Version", "1.44")
			_, _ = w.Write([]byte("OK"))
		case "/containers/json":
			writeJSON(w, 200, []map[string]any{{"Id": id, "NetworkSettings": map[string]any{"Networks": map[string]any{"app_default": map[string]any{"IPAddress": "172.20.0.2"}}}}})
		case "/containers/" + id + "/json":
			writeJSON(w, 200, map[string]any{"Id": id, "Config": map[string]any{}, "State": map[string]any{"Status": "running", "Running": true, "StartedAt": time.Now().Add(-time.Hour).Format(time.RFC3339Nano)}, "HostConfig": map[string]any{"Memory": 0}, "RestartCount": 0})
		case "/containers/" + id + "/stats":
			writeJSON(w, 200, map[string]any{"read": time.Now(), "memory_stats": map[string]any{"usage": 100, "stats": map[string]int{"inactive_file": 40}}})
		case "/events":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"Type": "container", "Action": "start", "timeNano": time.Now().UnixNano(), "Actor": map[string]any{"ID": id}})
			if r.URL.Query().Get("until") == "" {
				w.(http.Flusher).Flush()
				<-r.Context().Done()
				canceled <- struct{}{}
			}
		default:
			http.NotFound(w, r)
		}
	}))
	server.Config.ErrorLog = log.New(io.Discard, "", 0)
	server.TLS = &tls.Config{Certificates: []tls.Certificate{certificate}, ClientCAs: roots, ClientAuth: tls.RequireAndVerifyClientCert, MinVersion: tls.VersionTLS12}
	server.StartTLS()
	defer server.Close()
	transport := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, Certificates: []tls.Certificate{certificate}, MinVersion: tls.VersionTLS12}}
	defer transport.CloseIdleConnections()
	cli, err := client.NewClientWithOpts(client.WithHost(strings.Replace(server.URL, "https://", "tcp://", 1)), client.WithScheme("https"), client.WithHTTPClient(&http.Client{Transport: transport}), client.WithAPIVersionNegotiation())
	if err != nil {
		t.Fatal(err)
	}
	defer cli.Close()
	a := &Adapter{client: cli}
	ctx := context.Background()
	if result, err := a.List(ctx); err != nil || len(result) != 1 || len(result[0].Networks) != 1 || result[0].Networks[0].IPAddress != "172.20.0.2" {
		t.Fatalf("mTLS list networks failed: %v", err)
	}
	if result, err := domain.ReadDiagnostics(ctx, a, id); err != nil || result.Runtime.MemoryLimitBytes == nil || *result.Runtime.MemoryLimitBytes != 0 {
		t.Fatalf("mTLS diagnostic failed: %v", err)
	}
	stream, err := domain.OpenNormalizedStats(ctx, a, id)
	if err != nil {
		t.Fatal(err)
	}
	var point domain.Metrics
	err = json.NewDecoder(stream).Decode(&point)
	_ = stream.Close()
	if err != nil || point.MemoryBytes != 60 {
		t.Fatalf("mTLS statistics failed: %v", err)
	}
	if result, err := domain.ReadContainerEvents(ctx, a, "tls", id, domain.EventQuery{Since: time.Now().Add(-time.Hour), Until: time.Now(), Limit: 200}); err != nil || len(result.Events) != 1 {
		t.Fatalf("mTLS history failed: %v", err)
	}
	watchCtx, cancel := context.WithTimeout(ctx, time.Second)
	_ = domain.WatchContainerEvents(watchCtx, a, "tls", id, time.Now().Add(-time.Minute), func(domain.ContainerEvent) error { cancel(); return nil })
	cancel()
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("mTLS event request was not canceled")
	}
	inventory, err := a.MonitoringInventory(ctx)
	if err != nil || len(inventory) != 1 || inventory[0].ID != id {
		t.Fatalf("mTLS monitoring inventory failed: %v", err)
	}
	monitorCtx, stopMonitor := context.WithTimeout(ctx, time.Second)
	seenMonitor := false
	_ = a.MonitoringEvents(monitorCtx, time.Now().Add(-time.Minute), func(e domain.ContainerEvent, _ monitor.Inventory) error {
		seenMonitor = e.ContainerID == id && e.Action == "start"
		stopMonitor()
		return nil
	})
	stopMonitor()
	if !seenMonitor {
		t.Fatal("mTLS monitoring event absent")
	}
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("mTLS monitoring stream did not close")
	}
	anonymousTransport := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}}
	defer anonymousTransport.CloseIdleConnections()
	anonymous := &http.Client{Transport: anonymousTransport, Timeout: time.Second}
	if response, err := anonymous.Get(server.URL + "/_ping"); err == nil {
		response.Body.Close()
		t.Fatal("mTLS Engine accepted a client without its certificate")
	}
}
