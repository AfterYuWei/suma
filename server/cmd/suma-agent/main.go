package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/gorilla/websocket"
	"github.com/suma/suma/server/internal/agenthub"
	"github.com/suma/suma/server/internal/agentwire"
)

var version = "dev"

type identity struct {
	NodeID     string `json:"node_id"`
	Credential string `json:"credential"`
}

type envelope struct {
	Data    identity `json:"data"`
	Message string   `json:"message"`
}

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := run(logger); err != nil {
		logger.Error("agent stopped", "error", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	base, err := url.Parse(strings.TrimRight(os.Getenv("SUMA_AGENT_SERVER_URL"), "/"))
	if err != nil || base.Scheme != "https" || base.Host == "" || base.User != nil || base.RawQuery != "" || base.Fragment != "" || (base.Path != "" && base.Path != "/") {
		return errors.New("SUMA_AGENT_SERVER_URL must be an HTTPS origin")
	}
	dataDir := os.Getenv("SUMA_AGENT_DATA_DIR")
	if dataDir == "" {
		dataDir = "/var/lib/suma-agent"
	}
	socketPath := os.Getenv("SUMA_AGENT_DOCKER_SOCKET")
	if socketPath == "" {
		socketPath = "/var/run/docker.sock"
	}
	if !filepath.IsAbs(socketPath) {
		return errors.New("Docker socket path must be absolute")
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return err
	}
	caPool, err := x509.SystemCertPool()
	if err != nil {
		return err
	}
	if path := os.Getenv("SUMA_AGENT_CA_FILE"); path != "" {
		pem, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if !caPool.AppendCertsFromPEM(pem) {
			return errors.New("invalid Agent CA file")
		}
	}
	tlsConfig := &tls.Config{RootCAs: caPool, MinVersion: tls.VersionTLS12}
	transport := &http.Transport{Proxy: http.ProxyFromEnvironment, TLSClientConfig: tlsConfig}
	httpClient := &http.Client{Transport: transport, Timeout: 15 * time.Second}
	dialer := &websocket.Dialer{TLSClientConfig: tlsConfig, HandshakeTimeout: 15 * time.Second, Proxy: http.ProxyFromEnvironment}
	credentialPath := filepath.Join(dataDir, "identity.json")
	id, err := readIdentity(credentialPath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if id.Credential == "" {
		token := enrollmentToken()
		if token == "" {
			return errors.New("SUMA_AGENT_TOKEN is required for first pairing")
		}
		id, err = enroll(httpClient, base, token)
		if err != nil {
			return err
		}
		if err := saveIdentity(credentialPath, id); err != nil {
			return err
		}
		logger.Info("agent paired", "node_id", id.NodeID)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	backoff := time.Second
	for ctx.Err() == nil {
		err := connect(ctx, logger, dialer, base, socketPath, id)
		if ctx.Err() != nil {
			break
		}
		var unauthorized *authError
		if errors.As(err, &unauthorized) {
			if token := enrollmentToken(); token != "" {
				if replacement, enrollErr := enroll(httpClient, base, token); enrollErr == nil {
					if saveErr := saveIdentity(credentialPath, replacement); saveErr == nil {
						id, backoff = replacement, time.Second
						logger.Info("agent paired again", "node_id", id.NodeID)
						continue
					}
				}
			}
		}
		logger.Warn("agent connection lost", "error", err)
		select {
		case <-ctx.Done():
		case <-time.After(backoff + time.Duration(time.Now().UnixNano()%int64(backoff/2+1))):
		}
		if backoff < 30*time.Second {
			backoff *= 2
		}
	}
	return nil
}

func enrollmentToken() string {
	return strings.TrimSpace(os.Getenv("SUMA_AGENT_TOKEN"))
}

type authError struct{ status int }

func (e *authError) Error() string {
	return fmt.Sprintf("Agent authentication failed: HTTP %d", e.status)
}

func readIdentity(path string) (identity, error) {
	value, err := os.ReadFile(path)
	if err != nil {
		return identity{}, err
	}
	var id identity
	if err := json.Unmarshal(value, &id); err != nil {
		return id, err
	}
	if id.NodeID == "" || id.Credential == "" {
		return id, errors.New("incomplete Agent identity")
	}
	return id, nil
}

func saveIdentity(path string, id identity) error {
	value, err := json.Marshal(id)
	if err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".identity-*")
	if err != nil {
		return err
	}
	defer os.Remove(temp.Name())
	if err := temp.Chmod(0o600); err != nil {
		_ = temp.Close()
		return err
	}
	if _, err := temp.Write(value); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(temp.Name(), path)
}

func enroll(client *http.Client, base *url.URL, token string) (identity, error) {
	if token == "" {
		return identity{}, errors.New("empty Agent enrollment token")
	}
	address := *base
	address.Path = "/api/v1/agents/enroll"
	body, _ := json.Marshal(map[string]any{"protocol": agentwire.ProtocolVersion, "version": version})
	req, err := http.NewRequest(http.MethodPost, address.String(), bytes.NewReader(body))
	if err != nil {
		return identity{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	response, err := client.Do(req)
	if err != nil {
		return identity{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return identity{}, fmt.Errorf("Agent enrollment rejected: HTTP %d", response.StatusCode)
	}
	var result envelope
	if err := json.NewDecoder(io.LimitReader(response.Body, 4096)).Decode(&result); err != nil {
		return identity{}, err
	}
	if result.Data.NodeID == "" || result.Data.Credential == "" {
		return identity{}, errors.New("invalid enrollment response")
	}
	return result.Data, nil
}

func agentURL(base *url.URL, path string) string {
	address := *base
	address.Scheme = "wss"
	address.Path = path
	return address.String()
}

func headers(id identity) http.Header {
	value := make(http.Header)
	value.Set("Authorization", "Bearer "+id.Credential)
	value.Set("X-SUMA-Agent-Node-ID", id.NodeID)
	value.Set("X-SUMA-Agent-Protocol", strconv.Itoa(agentwire.ProtocolVersion))
	value.Set("X-SUMA-Agent-Version", version)
	return value
}

func connect(ctx context.Context, logger *slog.Logger, dialer *websocket.Dialer, base *url.URL, socketPath string, id identity) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	ws, response, err := dialer.DialContext(ctx, agentURL(base, "/ws/agents/control"), headers(id))
	if err != nil {
		if response != nil {
			if response.StatusCode == http.StatusUnauthorized {
				return &authError{status: response.StatusCode}
			}
			return fmt.Errorf("Agent control connection: HTTP %d: %w", response.StatusCode, err)
		}
		return err
	}
	defer ws.Close()
	logger.Info("agent connected", "node_id", id.NodeID)
	_ = ws.SetReadDeadline(time.Now().Add(45 * time.Second))
	ws.SetPingHandler(func(value string) error {
		if err := ws.SetReadDeadline(time.Now().Add(45 * time.Second)); err != nil {
			return err
		}
		return ws.WriteControl(websocket.PongMessage, []byte(value), time.Now().Add(5*time.Second))
	})
	var active sync.WaitGroup
	semaphore := make(chan struct{}, agentwire.MaxStreams)
	defer func() { cancel(); active.Wait() }()
	for {
		_, value, err := ws.ReadMessage()
		if err != nil {
			return err
		}
		message, err := agenthub.DecodeControl(value)
		if err != nil {
			return err
		}
		select {
		case semaphore <- struct{}{}:
		case <-ctx.Done():
			return ctx.Err()
		}
		active.Add(1)
		go func() {
			defer active.Done()
			defer func() { <-semaphore }()
			if err := serveStream(ctx, dialer, base, socketPath, id, message.ID); err != nil {
				logger.Debug("agent stream ended", "error", err)
			}
		}()
	}
}

func serveStream(ctx context.Context, dialer *websocket.Dialer, base *url.URL, socketPath string, id identity, streamID string) error {
	socket, err := net.DialTimeout("unix", socketPath, 10*time.Second)
	if err != nil {
		return err
	}
	address := agentURL(base, "/ws/agents/streams/"+streamID)
	ws, _, err := dialer.DialContext(ctx, address, headers(id))
	if err != nil {
		_ = socket.Close()
		return err
	}
	return agentwire.Bridge(ctx, socket, ws)
}
