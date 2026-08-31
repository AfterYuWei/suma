package node

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/suma/suma/server/internal/compose"
	"github.com/suma/suma/server/internal/database"
	"github.com/suma/suma/server/internal/docker"
	"github.com/suma/suma/server/internal/secret"
	"github.com/suma/suma/server/internal/task"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	ConnectionUnix = "unix"
	ConnectionTCP  = "tcp"
	TLSRequired    = "required"
	TLSDisabled    = "disabled"
)

var validID = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

var tailscaleIPv4 = &net.IPNet{
	IP:   net.IPv4(100, 64, 0, 0),
	Mask: net.CIDRMask(10, 32),
}

type Input struct {
	Name                  string `json:"name"`
	ConnectionType        string `json:"connection_type"`
	Endpoint              string `json:"endpoint"`
	TLSMode               string `json:"tls_mode"`
	TLSCredentialID       *uint  `json:"tls_credential_id"`
	PlaintextConfirmation string `json:"plaintext_confirmation"`
	Enabled               bool   `json:"enabled"`
}

type View struct {
	ID              string     `json:"id"`
	Name            string     `json:"name"`
	ConnectionType  string     `json:"connection_type"`
	Endpoint        string     `json:"endpoint"`
	TLSMode         string     `json:"tls_mode"`
	TLSCredentialID *uint      `json:"tls_credential_id,omitempty"`
	Enabled         bool       `json:"enabled"`
	EngineID        string     `json:"engine_id,omitempty"`
	EngineVersion   string     `json:"engine_version,omitempty"`
	Status          string     `json:"status"`
	LastError       string     `json:"last_error,omitempty"`
	LastLatencyMS   int64      `json:"last_latency_ms,omitempty"`
	LastCheckedAt   *time.Time `json:"last_checked_at,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

type TLSCredentialInput struct {
	Name              string   `json:"name"`
	CA                string   `json:"ca"`
	Certificate       string   `json:"certificate"`
	PrivateKey        string   `json:"private_key"`
	AuthorizedNodeIDs []string `json:"authorized_node_ids"`
}

type TLSCredentialView struct {
	ID                uint       `json:"id"`
	Name              string     `json:"name"`
	Fingerprint       string     `json:"fingerprint"`
	AuthorizedNodeIDs []string   `json:"authorized_node_ids"`
	CreatedAt         time.Time  `json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
	LastUsedAt        *time.Time `json:"last_used_at,omitempty"`
}

type cachedClient struct {
	updatedAt time.Time
	client    *docker.Adapter
}

type Service struct {
	db          *gorm.DB
	secrets     *secret.Store
	mu          sync.Mutex
	clients     map[string]cachedClient
	retired     []*docker.Adapter
	probeCancel context.CancelFunc
	probeWG     sync.WaitGroup
}

func (s *Service) Start() {
	s.mu.Lock()
	if s.probeCancel != nil {
		s.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.probeCancel = cancel
	s.probeWG.Add(1)
	s.mu.Unlock()
	go func() {
		defer s.probeWG.Done()
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		s.ProbeAll(ctx)
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.ProbeAll(ctx)
			}
		}
	}()
}

func (s *Service) Stop() {
	s.mu.Lock()
	cancel := s.probeCancel
	s.probeCancel = nil
	s.mu.Unlock()
	if cancel != nil {
		cancel()
		s.probeWG.Wait()
	}
}

func NewService(db *gorm.DB, secrets *secret.Store, bootstrapHost string) (*Service, error) {
	s := &Service{db: db, secrets: secrets, clients: map[string]cachedClient{}}
	var count int64
	if err := db.Model(&database.Node{}).Count(&count).Error; err != nil {
		return nil, err
	}
	if count == 0 {
		connection := ConnectionUnix
		tlsMode := TLSDisabled
		if strings.HasPrefix(bootstrapHost, "tcp://") {
			connection = ConnectionTCP
			tlsMode = TLSRequired
		}
		row := database.Node{ID: "local", Name: "Local", ConnectionType: connection, Endpoint: bootstrapHost, TLSMode: tlsMode, AllowedBindRootsJSON: "[]", Enabled: true, Status: "unknown"}
		if err := db.Create(&row).Error; err != nil {
			return nil, fmt.Errorf("create default Docker node: %w", err)
		}
	}
	// Upgrade the already-reserved local ownership columns.
	if err := db.Model(&database.DeliveryProject{}).Where("node_id = '' OR node_id IS NULL").Update("node_id", "local").Error; err != nil {
		return nil, err
	}
	var local database.Node
	if err := db.Where("id = ?", "local").First(&local).Error; err != nil {
		return nil, err
	}
	if err := db.Model(&database.Task{}).Where("scope = ? AND (node_id = '' OR node_id IS NULL)", task.ScopeNode).Updates(map[string]any{"node_id": local.ID, "node_name": local.Name}).Error; err != nil {
		return nil, err
	}
	if err := db.Model(&database.Task{}).Where("scope = ? AND node_id = ? AND node_name = ''", task.ScopeNode, local.ID).Update("node_name", local.Name).Error; err != nil {
		return nil, err
	}
	if err := db.Model(&database.AuditLog{}).Where("scope = ? AND (node_id = '' OR node_id IS NULL)", task.ScopeNode).Updates(map[string]any{"node_id": local.ID, "node_name": local.Name}).Error; err != nil {
		return nil, err
	}
	if err := db.Model(&database.AuditLog{}).Where("scope = ? AND node_id = ? AND node_name = ''", task.ScopeNode, local.ID).Update("node_name", local.Name).Error; err != nil {
		return nil, err
	}
	var projects []database.DeliveryProject
	if err := db.Find(&projects).Error; err != nil {
		return nil, err
	}
	for _, project := range projects {
		nodeID := project.NodeID
		if nodeID == "" {
			nodeID = "local"
		}
		if err := db.Clauses(clause.OnConflict{DoNothing: true}).Create(&database.DeliveryProjectNode{ProjectID: project.ID, NodeID: nodeID}).Error; err != nil {
			return nil, err
		}
	}
	return s, nil
}

func (s *Service) List(ctx context.Context) ([]View, error) {
	var rows []database.Node
	if err := s.db.WithContext(ctx).Order("name ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	result := make([]View, 0, len(rows))
	for _, row := range rows {
		result = append(result, view(row))
	}
	return result, nil
}

func (s *Service) Get(ctx context.Context, id string) (View, error) {
	row, err := s.row(ctx, id)
	if err != nil {
		return View{}, err
	}
	return view(row), nil
}

func (s *Service) Create(ctx context.Context, input Input) (View, error) {
	id, err := newID(input.Name)
	if err != nil {
		return View{}, err
	}
	row, err := s.prepare(ctx, id, input)
	if err != nil {
		return View{}, err
	}
	row.ID = id
	client, info, latency, err := s.connect(ctx, row)
	if err != nil {
		return View{}, err
	}
	defer client.Close()
	row.EngineID, row.EngineVersion, row.Status, row.LastLatencyMS = info.ID, info.ServerVersion, "online", latency
	now := time.Now()
	row.LastCheckedAt = &now
	if err := s.ensureUniqueEngine(ctx, "", info.ID); err != nil {
		return View{}, err
	}
	if err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&row).Error; err != nil {
			return err
		}
		if row.TLSCredentialID != nil {
			return tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&database.DockerTLSCredentialNode{CredentialID: *row.TLSCredentialID, NodeID: row.ID}).Error
		}
		return nil
	}); err != nil {
		return View{}, err
	}
	return view(row), nil
}

func (s *Service) Update(ctx context.Context, id string, input Input) (View, error) {
	current, err := s.row(ctx, id)
	if err != nil {
		return View{}, err
	}
	row, err := s.prepare(ctx, id, input)
	if err != nil {
		return View{}, err
	}
	row.ID, row.CreatedAt = current.ID, current.CreatedAt
	client, info, latency, err := s.connect(ctx, row)
	if err != nil {
		return View{}, err
	}
	defer client.Close()
	if err := s.ensureUniqueEngine(ctx, id, info.ID); err != nil {
		return View{}, err
	}
	now := time.Now()
	row.EngineID, row.EngineVersion, row.Status, row.LastLatencyMS, row.LastCheckedAt, row.LastError = info.ID, info.ServerVersion, "online", latency, &now, ""
	if err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&database.Node{}).Where("id = ?", id).Updates(map[string]any{
			"name": row.Name, "connection_type": row.ConnectionType, "endpoint": row.Endpoint, "tls_mode": row.TLSMode,
			"tls_credential_id": row.TLSCredentialID, "enabled": row.Enabled,
			"engine_id": row.EngineID, "engine_version": row.EngineVersion, "status": row.Status, "last_error": "",
			"last_latency_ms": latency, "last_checked_at": now,
		}).Error; err != nil {
			return err
		}
		if row.TLSCredentialID != nil {
			return tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&database.DockerTLSCredentialNode{CredentialID: *row.TLSCredentialID, NodeID: id}).Error
		}
		return nil
	}); err != nil {
		return View{}, err
	}
	s.invalidate(id)
	return s.Get(ctx, id)
}

func (s *Service) Delete(ctx context.Context, id string) error {
	if id == "local" {
		return errors.New("the default local node cannot be deleted")
	}
	checks := []struct {
		model any
		query string
	}{
		{&database.DeliveryProjectNode{}, "node_id = ?"},
		{&database.GitCredentialNode{}, "node_id = ?"}, {&database.RegistryCredentialNode{}, "node_id = ?"}, {&database.DockerTLSCredentialNode{}, "node_id = ?"},
	}
	for _, check := range checks {
		var count int64
		if err := s.db.WithContext(ctx).Model(check.model).Where(check.query, id).Count(&count).Error; err != nil {
			return err
		}
		if count > 0 {
			return errors.New("node is still referenced; remove projects and credential grants first")
		}
	}
	result := s.db.WithContext(ctx).Delete(&database.Node{}, "id = ?", id)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	s.invalidate(id)
	return nil
}

func (s *Service) Test(ctx context.Context, id string) (docker.Info, error) {
	started := time.Now()
	client, err := s.Runtime(ctx, id)
	if err != nil {
		s.recordProbe(id, started, docker.Info{}, err)
		return docker.Info{}, err
	}
	info, err := client.Info(ctx)
	s.recordProbe(id, started, info, err)
	return info, err
}

func (s *Service) Runtime(ctx context.Context, id string) (*docker.Adapter, error) {
	row, err := s.row(ctx, id)
	if err != nil {
		return nil, err
	}
	if !row.Enabled {
		return nil, errors.New("Docker node is disabled")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if cached, ok := s.clients[id]; ok && cached.updatedAt.Equal(row.UpdatedAt) {
		return cached.client, nil
	}
	client, _, _, err := s.connect(ctx, row)
	if err != nil {
		return nil, err
	}
	if old, ok := s.clients[id]; ok {
		// Existing operations may have captured this client. Retire it until
		// process shutdown instead of tearing down active Docker streams.
		s.retired = append(s.retired, old.client)
	}
	s.clients[id] = cachedClient{updatedAt: row.UpdatedAt, client: client}
	return client, nil
}

func (s *Service) ComposeTarget(ctx context.Context, id string) (compose.Target, View, error) {
	row, err := s.row(ctx, id)
	if err != nil {
		return compose.Target{}, View{}, err
	}
	if !row.Enabled {
		return compose.Target{}, view(row), errors.New("Docker node is disabled")
	}
	target := compose.Target{NodeID: row.ID, NodeName: row.Name, Host: row.Endpoint, TLSRequired: row.ConnectionType == ConnectionTCP && row.TLSMode == TLSRequired}
	if target.TLSRequired {
		if row.TLSCredentialID == nil {
			return compose.Target{}, view(row), errors.New("Docker TLS credential is required")
		}
		var credential database.DockerTLSCredential
		if err := s.db.WithContext(ctx).First(&credential, *row.TLSCredentialID).Error; err != nil {
			return compose.Target{}, view(row), err
		}
		var grants int64
		if err := s.db.WithContext(ctx).Model(&database.DockerTLSCredentialNode{}).Where("credential_id = ? AND node_id = ?", credential.ID, row.ID).Count(&grants).Error; err != nil {
			return compose.Target{}, view(row), err
		}
		if grants == 0 {
			return compose.Target{}, view(row), errors.New("Docker TLS credential is not authorized for this node")
		}
		target.CA, err = s.secrets.Decrypt(credential.CACiphertext)
		if err != nil {
			return compose.Target{}, view(row), err
		}
		target.Certificate, err = s.secrets.Decrypt(credential.CertificateCiphertext)
		if err != nil {
			return compose.Target{}, view(row), err
		}
		target.PrivateKey, err = s.secrets.Decrypt(credential.PrivateKeyCiphertext)
		if err != nil {
			return compose.Target{}, view(row), err
		}
	}
	return target, view(row), nil
}
func (s *Service) ResolveComposeTarget(ctx context.Context, id string) (compose.Target, error) {
	target, _, err := s.ComposeTarget(ctx, id)
	return target, err
}

func (s *Service) Close() error {
	s.Stop()
	s.mu.Lock()
	defer s.mu.Unlock()
	var first error
	for id, cached := range s.clients {
		if err := cached.client.Close(); err != nil && first == nil {
			first = err
		}
		delete(s.clients, id)
	}
	for _, client := range s.retired {
		if err := client.Close(); err != nil && first == nil {
			first = err
		}
	}
	s.retired = nil
	return first
}

func (s *Service) ProbeAll(ctx context.Context) {
	rows, err := s.List(ctx)
	if err != nil {
		return
	}
	for _, row := range rows {
		if row.Enabled {
			probe, cancel := context.WithTimeout(ctx, 5*time.Second)
			_, _ = s.Test(probe, row.ID)
			cancel()
		}
	}
}

func (s *Service) row(ctx context.Context, id string) (database.Node, error) {
	if !validID.MatchString(id) {
		return database.Node{}, errors.New("invalid node ID")
	}
	var row database.Node
	return row, s.db.WithContext(ctx).Where("id = ?", id).First(&row).Error
}

func (s *Service) prepare(ctx context.Context, id string, input Input) (database.Node, error) {
	input.Name, input.Endpoint = strings.TrimSpace(input.Name), strings.TrimSpace(input.Endpoint)
	if input.Name == "" || len(input.Name) > 128 {
		return database.Node{}, errors.New("node name is required and must not exceed 128 characters")
	}
	if err := validateEndpoint(input.ConnectionType, input.Endpoint, input.TLSMode, input.TLSCredentialID); err != nil {
		return database.Node{}, err
	}
	if input.ConnectionType == ConnectionTCP && input.TLSMode == TLSDisabled {
		parsed, _ := url.Parse(input.Endpoint)
		host, _, _ := net.SplitHostPort(parsed.Host)
		if strings.TrimSpace(input.PlaintextConfirmation) != host {
			return database.Node{}, fmt.Errorf("type Docker endpoint IP %q to confirm plaintext TCP", host)
		}
	}
	if input.TLSCredentialID != nil {
		var count int64
		if err := s.db.WithContext(ctx).Model(&database.DockerTLSCredential{}).Where("id = ?", *input.TLSCredentialID).Count(&count).Error; err != nil {
			return database.Node{}, err
		}
		if count == 0 {
			return database.Node{}, errors.New("Docker TLS credential not found")
		}
	}
	return database.Node{ID: id, Name: input.Name, ConnectionType: input.ConnectionType, Endpoint: input.Endpoint, TLSMode: input.TLSMode, TLSCredentialID: input.TLSCredentialID, AllowedBindRootsJSON: "[]", Enabled: input.Enabled, Status: "unknown"}, nil
}

func validateEndpoint(connection, endpoint, tlsMode string, credentialID *uint) error {
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return errors.New("invalid Docker endpoint")
	}
	switch connection {
	case ConnectionUnix:
		if parsed.Scheme != "unix" || parsed.Host != "" || parsed.Path == "" || !filepath.IsAbs(parsed.Path) || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.User != nil {
			return errors.New("Unix nodes require an absolute unix:// endpoint")
		}
		if tlsMode != TLSDisabled || credentialID != nil {
			return errors.New("Unix nodes do not use TLS credentials")
		}
	case ConnectionTCP:
		if parsed.Scheme != "tcp" || parsed.Host == "" || (parsed.Path != "" && parsed.Path != "/") || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.User != nil {
			return errors.New("TCP nodes require a tcp://host:port endpoint")
		}
		host, _, err := net.SplitHostPort(parsed.Host)
		if err != nil {
			return errors.New("TCP nodes require an explicit port")
		}
		if tlsMode == TLSRequired && credentialID == nil {
			return errors.New("mTLS TCP nodes require a Docker TLS credential")
		}
		if tlsMode == TLSDisabled {
			ip := net.ParseIP(host)
			if !strings.EqualFold(host, "localhost") && (ip == nil || (!ip.IsLoopback() && !ip.IsPrivate() && !tailscaleIPv4.Contains(ip))) {
				return errors.New("plaintext Docker TCP is allowed only on loopback, private network, or Tailscale addresses")
			}
			if credentialID != nil {
				return errors.New("plaintext TCP nodes cannot select a TLS credential")
			}
		} else if tlsMode != TLSRequired {
			return errors.New("TLS mode must be required or disabled")
		}
	default:
		return errors.New("connection type must be unix or tcp")
	}
	return nil
}

func (s *Service) connect(ctx context.Context, row database.Node) (*docker.Adapter, docker.Info, int64, error) {
	var client *docker.Adapter
	var err error
	if row.ConnectionType == ConnectionTCP && row.TLSMode == TLSRequired {
		if row.TLSCredentialID == nil {
			return nil, docker.Info{}, 0, errors.New("Docker TLS credential is required")
		}
		var credential database.DockerTLSCredential
		if err := s.db.WithContext(ctx).First(&credential, *row.TLSCredentialID).Error; err != nil {
			return nil, docker.Info{}, 0, errors.New("Docker TLS credential not found")
		}
		var grant int64
		if err := s.db.WithContext(ctx).Model(&database.DockerTLSCredentialNode{}).Where("credential_id = ? AND node_id = ?", credential.ID, row.ID).Count(&grant).Error; err != nil {
			return nil, docker.Info{}, 0, err
		}
		// New nodes are granted atomically after this preflight, so absence is
		// accepted until the node row exists. Existing nodes must remain granted.
		var existing int64
		_ = s.db.Model(&database.Node{}).Where("id = ?", row.ID).Count(&existing).Error
		if existing > 0 && grant == 0 {
			return nil, docker.Info{}, 0, errors.New("Docker TLS credential is not authorized for this node")
		}
		ca, err := s.secrets.Decrypt(credential.CACiphertext)
		if err != nil {
			return nil, docker.Info{}, 0, err
		}
		cert, err := s.secrets.Decrypt(credential.CertificateCiphertext)
		if err != nil {
			return nil, docker.Info{}, 0, err
		}
		key, err := s.secrets.Decrypt(credential.PrivateKeyCiphertext)
		if err != nil {
			return nil, docker.Info{}, 0, err
		}
		client, err = docker.NewTLS(row.Endpoint, ca, cert, key)
	} else {
		client, err = docker.New(row.Endpoint)
	}
	if err != nil {
		return nil, docker.Info{}, 0, err
	}
	started := time.Now()
	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := client.Ping(pingCtx); err != nil {
		_ = client.Close()
		return nil, docker.Info{}, 0, err
	}
	info, err := client.Info(pingCtx)
	if err != nil {
		_ = client.Close()
		return nil, docker.Info{}, 0, err
	}
	return client, info, time.Since(started).Milliseconds(), nil
}

func (s *Service) ensureUniqueEngine(ctx context.Context, excludeID, engineID string) error {
	if engineID == "" {
		return nil
	}
	query := s.db.WithContext(ctx).Model(&database.Node{}).Where("engine_id = ?", engineID)
	if excludeID != "" {
		query = query.Where("id <> ?", excludeID)
	}
	var count int64
	if err := query.Count(&count).Error; err != nil {
		return err
	}
	if count > 0 {
		return errors.New("this Docker Engine is already registered")
	}
	return nil
}

func (s *Service) recordProbe(id string, started time.Time, info docker.Info, probeErr error) {
	now := time.Now()
	updates := map[string]any{"last_checked_at": now, "last_latency_ms": time.Since(started).Milliseconds()}
	if probeErr != nil {
		updates["status"], updates["last_error"] = "offline", probeErr.Error()
	} else {
		updates["status"], updates["last_error"], updates["engine_id"], updates["engine_version"] = "online", "", info.ID, info.ServerVersion
	}
	_ = s.db.Model(&database.Node{}).Where("id = ?", id).Updates(updates).Error
}

func (s *Service) invalidate(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if cached, ok := s.clients[id]; ok {
		s.retired = append(s.retired, cached.client)
		delete(s.clients, id)
	}
}

func view(row database.Node) View {
	return View{ID: row.ID, Name: row.Name, ConnectionType: row.ConnectionType, Endpoint: row.Endpoint, TLSMode: row.TLSMode, TLSCredentialID: row.TLSCredentialID, Enabled: row.Enabled, EngineID: row.EngineID, EngineVersion: row.EngineVersion, Status: row.Status, LastError: row.LastError, LastLatencyMS: row.LastLatencyMS, LastCheckedAt: row.LastCheckedAt, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt}
}

func newID(name string) (string, error) {
	base := strings.ToLower(strings.TrimSpace(name))
	base = regexp.MustCompile(`[^a-z0-9]+`).ReplaceAllString(base, "-")
	base = strings.Trim(base, "-")
	if len(base) > 48 {
		base = base[:48]
	}
	if base == "" {
		base = "node"
	}
	value := make([]byte, 4)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return base + "-" + hex.EncodeToString(value), nil
}
