package agenthub

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/suma/suma/server/internal/agentwire"
)

type ControlMessage struct {
	Type string `json:"type"`
	ID   string `json:"id,omitempty"`
}

var validNodeID = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

type pending struct {
	conn    net.Conn
	session *session
	timer   *time.Timer
}

type session struct {
	ws      *websocket.Conn
	mu      sync.Mutex
	done    chan struct{}
	streams map[*websocket.Conn]net.Conn
}

func (s *session) send(message ControlMessage) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_ = s.ws.SetWriteDeadline(time.Now().Add(5 * time.Second))
	return s.ws.WriteJSON(message)
}

type nodeProxy struct {
	listener net.Listener
	current  *session
	pending  map[string]*pending
	removed  bool
}

type Hub struct {
	mu           sync.Mutex
	directory    string
	nodes        map[string]*nodeProxy
	onDisconnect func(string)
	closed       bool
}

func New() (*Hub, error) {
	directory, err := os.MkdirTemp("", "suma-agent-")
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(directory, 0o700); err != nil {
		_ = os.RemoveAll(directory)
		return nil, err
	}
	return &Hub{directory: directory, nodes: map[string]*nodeProxy{}}, nil
}

func (h *Hub) SetDisconnect(callback func(string)) { h.onDisconnect = callback }

func (h *Hub) Endpoint(id string) (string, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return "", errors.New("agent hub is closed")
	}
	if !validNodeID.MatchString(id) {
		return "", errors.New("invalid agent node ID")
	}
	if _, ok := h.nodes[id]; !ok {
		path := filepath.Join(h.directory, id+".sock")
		listener, err := net.Listen("unix", path)
		if err != nil {
			return "", err
		}
		if err := os.Chmod(path, 0o600); err != nil {
			_ = listener.Close()
			return "", err
		}
		proxy := &nodeProxy{listener: listener, pending: map[string]*pending{}}
		h.nodes[id] = proxy
		go h.accept(id, proxy)
	}
	return "unix://" + filepath.Join(h.directory, id+".sock"), nil
}

func randomID() (string, error) {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return hex.EncodeToString(value), nil
}

func (h *Hub) accept(id string, proxy *nodeProxy) {
	for {
		conn, err := proxy.listener.Accept()
		if err != nil {
			return
		}
		h.mu.Lock()
		current := proxy.current
		streamCount := 0
		if current != nil {
			current.mu.Lock()
			streamCount = len(current.streams)
			current.mu.Unlock()
		}
		if current == nil || len(proxy.pending)+streamCount >= agentwire.MaxStreams {
			h.mu.Unlock()
			_ = conn.Close()
			continue
		}
		streamID, err := randomID()
		if err != nil {
			h.mu.Unlock()
			_ = conn.Close()
			continue
		}
		entry := &pending{conn: conn, session: current}
		entry.timer = time.AfterFunc(10*time.Second, func() {
			h.mu.Lock()
			if proxy.pending[streamID] == entry {
				delete(proxy.pending, streamID)
				_ = conn.Close()
			}
			h.mu.Unlock()
		})
		proxy.pending[streamID] = entry
		h.mu.Unlock()
		if err := current.send(ControlMessage{Type: "open", ID: streamID}); err != nil {
			h.mu.Lock()
			if proxy.pending[streamID] == entry {
				delete(proxy.pending, streamID)
				entry.timer.Stop()
				_ = conn.Close()
			}
			h.mu.Unlock()
		}
	}
}

// Attach starts the single active control connection for a node. The caller
// authenticates the Agent before calling this method.
func (h *Hub) Attach(id string, ws *websocket.Conn) (<-chan struct{}, error) {
	if _, err := h.Endpoint(id); err != nil {
		return nil, err
	}
	h.mu.Lock()
	proxy := h.nodes[id]
	if h.closed || proxy == nil || proxy.removed {
		h.mu.Unlock()
		return nil, errors.New("agent proxy is unavailable")
	}
	old := proxy.current
	current := &session{ws: ws, done: make(chan struct{}), streams: map[*websocket.Conn]net.Conn{}}
	proxy.current = current
	h.mu.Unlock()
	if old != nil {
		h.closeSession(id, old)
	}
	go h.control(id, current)
	return current.done, nil
}

func (h *Hub) control(id string, current *session) {
	defer h.closeSession(id, current)
	current.ws.SetReadLimit(1024)
	_ = current.ws.SetReadDeadline(time.Now().Add(45 * time.Second))
	current.ws.SetPongHandler(func(string) error { return current.ws.SetReadDeadline(time.Now().Add(45 * time.Second)) })
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	stop := make(chan struct{})
	go func() {
		defer close(stop)
		for {
			if _, _, err := current.ws.ReadMessage(); err != nil {
				return
			}
		}
	}()
	for {
		select {
		case <-current.done:
			return
		case <-stop:
			return
		case <-ticker.C:
			if err := current.ws.WriteControl(websocket.PingMessage, nil, time.Now().Add(5*time.Second)); err != nil {
				return
			}
		}
	}
}

func (h *Hub) closeSession(id string, current *session) {
	h.mu.Lock()
	proxy := h.nodes[id]
	active := proxy != nil && proxy.current == current
	if active {
		proxy.current = nil
	}
	if proxy != nil {
		for key, entry := range proxy.pending {
			if entry.session == current {
				entry.timer.Stop()
				_ = entry.conn.Close()
				delete(proxy.pending, key)
			}
		}
	}
	h.mu.Unlock()
	current.mu.Lock()
	select {
	case <-current.done:
	default:
		close(current.done)
	}
	_ = current.ws.Close()
	for ws, conn := range current.streams {
		_ = ws.Close()
		_ = conn.Close()
	}
	current.mu.Unlock()
	if active && h.onDisconnect != nil {
		h.onDisconnect(id)
	}
}

func (h *Hub) AttachStream(ctx context.Context, id, streamID string, ws *websocket.Conn) error {
	h.mu.Lock()
	proxy := h.nodes[id]
	if proxy == nil {
		h.mu.Unlock()
		return errors.New("unknown agent proxy")
	}
	entry := proxy.pending[streamID]
	if entry == nil || entry.session != proxy.current {
		h.mu.Unlock()
		return errors.New("agent stream is not pending")
	}
	delete(proxy.pending, streamID)
	entry.timer.Stop()
	entry.session.mu.Lock()
	entry.session.streams[ws] = entry.conn
	entry.session.mu.Unlock()
	h.mu.Unlock()
	defer func() {
		entry.session.mu.Lock()
		delete(entry.session.streams, ws)
		entry.session.mu.Unlock()
	}()
	return agentwire.Bridge(ctx, entry.conn, ws)
}

func (h *Hub) Connected(id string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	proxy := h.nodes[id]
	return proxy != nil && proxy.current != nil
}

func (h *Hub) Active(id string, done <-chan struct{}) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	proxy := h.nodes[id]
	return proxy != nil && proxy.current != nil && proxy.current.done == done
}

func (h *Hub) Detach(id string, done <-chan struct{}) {
	h.mu.Lock()
	proxy := h.nodes[id]
	var current *session
	if proxy != nil && proxy.current != nil && proxy.current.done == done {
		current = proxy.current
	}
	h.mu.Unlock()
	if current != nil {
		h.closeSession(id, current)
	}
}

func (h *Hub) Disconnect(id string) {
	h.mu.Lock()
	proxy := h.nodes[id]
	var current *session
	if proxy != nil {
		current = proxy.current
	}
	h.mu.Unlock()
	if current != nil {
		h.closeSession(id, current)
	}
}

func (h *Hub) Remove(id string) {
	h.mu.Lock()
	if proxy := h.nodes[id]; proxy != nil {
		proxy.removed = true
	}
	h.mu.Unlock()
	h.Disconnect(id)
	h.mu.Lock()
	proxy := h.nodes[id]
	delete(h.nodes, id)
	h.mu.Unlock()
	if proxy != nil {
		path := proxy.listener.Addr().String()
		_ = proxy.listener.Close()
		_ = os.Remove(path)
	}
}

func (h *Hub) Close() error {
	h.mu.Lock()
	h.closed = true
	ids := make([]string, 0, len(h.nodes))
	for id := range h.nodes {
		ids = append(ids, id)
	}
	h.mu.Unlock()
	for _, id := range ids {
		h.Disconnect(id)
		h.mu.Lock()
		if proxy := h.nodes[id]; proxy != nil {
			_ = proxy.listener.Close()
		}
		h.mu.Unlock()
	}
	return os.RemoveAll(h.directory)
}

func DecodeControl(payload []byte) (ControlMessage, error) {
	var message ControlMessage
	if err := json.Unmarshal(payload, &message); err != nil {
		return message, err
	}
	if message.Type != "open" || len(message.ID) != 32 {
		return message, fmt.Errorf("invalid agent control message")
	}
	return message, nil
}
