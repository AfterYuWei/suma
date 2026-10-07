package agenthub

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/suma/suma/server/internal/agentwire"
)

func TestAgentProxyCarriesLargeHalfClosedStream(t *testing.T) {
	hub, err := New()
	if err != nil {
		t.Fatal(err)
	}
	defer hub.Close()
	backendPath := filepath.Join(t.TempDir(), "docker.sock")
	backend, err := net.Listen("unix", backendPath)
	if err != nil {
		t.Fatal(err)
	}
	defer backend.Close()
	go func() {
		for {
			conn, err := backend.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				body, _ := io.ReadAll(conn)
				_, _ = conn.Write(append([]byte("reply:"), body...))
			}()
		}
	}()
	upgrade := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	controlReady := make(chan error, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := upgrade.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		if r.URL.Path == "/control" {
			done, err := hub.Attach("edge", ws)
			controlReady <- err
			if err == nil {
				<-done
			}
			return
		}
		_ = hub.AttachStream(r.Context(), "edge", strings.TrimPrefix(r.URL.Path, "/stream/"), ws)
	}))
	defer server.Close()
	address, _ := url.Parse(server.URL)
	address.Scheme = "ws"
	control, _, err := websocket.DefaultDialer.Dial(address.String()+"/control", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer control.Close()
	select {
	case err := <-controlReady:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Agent control connection did not register")
	}
	go func() {
		for {
			_, payload, err := control.ReadMessage()
			if err != nil {
				return
			}
			message, err := DecodeControl(payload)
			if err != nil {
				return
			}
			go func() {
				backendConn, err := net.Dial("unix", backendPath)
				if err != nil {
					return
				}
				stream, _, err := websocket.DefaultDialer.Dial(address.String()+"/stream/"+message.ID, nil)
				if err != nil {
					_ = backendConn.Close()
					return
				}
				_ = agentwire.Bridge(context.Background(), backendConn, stream)
			}()
		}
	}()
	endpoint, err := hub.Endpoint("edge")
	if err != nil {
		t.Fatal(err)
	}
	conn, err := net.Dial("unix", strings.TrimPrefix(endpoint, "unix://"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	value := bytes.Repeat([]byte("stream-data-"), 25000)
	if _, err := conn.Write(value); err != nil {
		t.Fatal(err)
	}
	if err := conn.(*net.UnixConn).CloseWrite(); err != nil {
		t.Fatal(err)
	}
	response, err := io.ReadAll(conn)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(response, append([]byte("reply:"), value...)) {
		t.Fatalf("stream bytes differ: got %d bytes", len(response))
	}
}

func TestAgentDisconnectClosesActiveProxyStream(t *testing.T) {
	hub, err := New()
	if err != nil {
		t.Fatal(err)
	}
	defer hub.Close()
	upgrade := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	controlReady := make(chan error, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := upgrade.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		if r.URL.Path == "/control" {
			done, err := hub.Attach("edge", ws)
			controlReady <- err
			if err == nil {
				<-done
			}
			return
		}
		_ = hub.AttachStream(r.Context(), "edge", strings.TrimPrefix(r.URL.Path, "/stream/"), ws)
	}))
	defer server.Close()
	address := "ws" + strings.TrimPrefix(server.URL, "http")
	control, _, err := websocket.DefaultDialer.Dial(address+"/control", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer control.Close()
	select {
	case err := <-controlReady:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Agent control connection did not register")
	}
	endpoint, err := hub.Endpoint("edge")
	if err != nil {
		t.Fatal(err)
	}
	conn, err := net.Dial("unix", strings.TrimPrefix(endpoint, "unix://"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = control.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, payload, err := control.ReadMessage()
	if err != nil {
		t.Fatal(err)
	}
	message, err := DecodeControl(payload)
	if err != nil {
		t.Fatal(err)
	}
	stream, _, err := websocket.DefaultDialer.Dial(address+"/stream/"+message.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	deadline := time.Now().Add(5 * time.Second)
	attached := false
	for time.Now().Before(deadline) {
		hub.mu.Lock()
		active := hub.nodes["edge"].current
		active.mu.Lock()
		count := len(active.streams)
		active.mu.Unlock()
		hub.mu.Unlock()
		if count == 1 {
			attached = true
			break
		}
		time.Sleep(time.Millisecond)
	}
	if !attached {
		t.Fatal("Agent stream did not attach")
	}
	hub.Disconnect("edge")
	_ = conn.SetReadDeadline(time.Now().Add(time.Second))
	if _, err := conn.Read(make([]byte, 1)); err == nil {
		t.Fatal("Docker proxy stream remained open after Agent disconnect")
	}
}

func TestAgentReplacementOnlyDetachesItsOwnSession(t *testing.T) {
	hub, err := New()
	if err != nil {
		t.Fatal(err)
	}
	defer hub.Close()
	ready := make(chan (<-chan struct{}), 2)
	upgrade := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := upgrade.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		done, err := hub.Attach("edge", ws)
		if err == nil {
			ready <- done
			<-done
		}
	}))
	defer server.Close()
	address := "ws" + strings.TrimPrefix(server.URL, "http")
	first, _, err := websocket.DefaultDialer.Dial(address, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	firstDone := <-ready
	second, _, err := websocket.DefaultDialer.Dial(address, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	secondDone := <-ready
	select {
	case <-firstDone:
	case <-time.After(time.Second):
		t.Fatal("replaced Agent session remained active")
	}
	if hub.Active("edge", firstDone) || !hub.Active("edge", secondDone) {
		t.Fatal("Agent replacement selected the wrong active session")
	}
	hub.Detach("edge", firstDone)
	if !hub.Active("edge", secondDone) {
		t.Fatal("old Agent activation detached the replacement session")
	}
	hub.Detach("edge", secondDone)
	if hub.Connected("edge") {
		t.Fatal("active Agent session remained after detach")
	}
}
