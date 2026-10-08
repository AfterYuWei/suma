package ai

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/suma/suma/server/internal/audit"
	"github.com/suma/suma/server/internal/database"
	"github.com/suma/suma/server/internal/event"
	"github.com/suma/suma/server/internal/redact"
	"github.com/suma/suma/server/internal/secret"
	"github.com/suma/suma/server/internal/task"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type Service struct {
	db            *gorm.DB
	secrets       *secret.Store
	tasks         *task.Service
	deps          Dependencies
	mu            sync.Mutex
	cfg           Settings
	key           string
	cancels       map[string]context.CancelFunc
	wg            sync.WaitGroup
	stopped       bool
	querySlots    chan struct{}
	runtimeCancel context.CancelFunc
	wake          chan struct{}
	owner         string
}

func NewService(db *gorm.DB, store *secret.Store, tasks *task.Service, deps Dependencies) (*Service, error) {
	if deps.Now == nil {
		deps.Now = time.Now
	}
	if deps.Model == nil {
		deps.Model = HTTPModel{}
	}
	clock := deps.Now
	deps.Now = func() time.Time { return clock().UTC() }
	db = db.Session(&gorm.Session{NowFunc: deps.Now})
	if deps.Audit == nil {
		deps.Audit = audit.NewService(db)
	}
	s := &Service{db: db, secrets: store, tasks: tasks, deps: deps, cfg: DefaultSettings(), cancels: map[string]context.CancelFunc{}, querySlots: make(chan struct{}, 2)}
	var row database.Setting
	if err := db.First(&row, "key = ?", "internal.ai.settings").Error; err == nil {
		if json.Unmarshal([]byte(row.Value), &s.cfg) != nil {
			return nil, errors.New("invalid stored AI settings")
		}
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	// GORM otherwise retains the first Setting's primary key in this query.
	row = database.Setting{}
	if err := db.First(&row, "key = ?", "internal.ai.key").Error; err == nil {
		cipher, err := hex.DecodeString(row.Value)
		if err != nil {
			return nil, err
		}
		s.key, err = store.Decrypt(cipher)
		if err != nil {
			return nil, err
		}
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	if s.cfg.Protocol != ProtocolResponses {
		return nil, errors.New("stored AI configuration must use Responses")
	}

	// Interrupted mutations are recorded, never automatically replayed.
	if err := db.Model(&database.AIOperation{}).Where("status IN ?", []string{"queued", "running"}).Updates(map[string]any{"status": "interrupted", "result": "SUMA restarted; inspect actual state before creating a new proposal"}).Error; err != nil {
		return nil, err
	}
	var interrupted []database.AIRun
	if err := db.Where("status = ?", "running").Find(&interrupted).Error; err != nil {
		return nil, err
	}
	for _, run := range interrupted {
		if err := db.Transaction(func(tx *gorm.DB) error {
			if run.ConversationID != "" {
				if err := s.lockConversation(context.Background(), tx, run.ConversationID); err != nil {
					return err
				}
			}
			changed := tx.Model(&database.AIRun{}).Where("id = ? AND status = ?", run.ID, "running").Updates(map[string]any{"status": "paused", "error": "SUMA restarted; inspect actual state or resume from a saved checkpoint"})
			if changed.Error != nil || changed.RowsAffected == 0 || run.ConversationID == "" {
				return changed.Error
			}
			return s.eventTx(context.Background(), tx, run.ConversationID, run.ID, "run.paused", map[string]string{"run_id": run.ID})
		}); err != nil {
			return nil, err
		}
	}
	if err := db.Model(&database.AIConversation{}).Where("source = ?", "chat").Update("chat_lease_until", nil).Error; err != nil {
		return nil, err
	}
	s.startRuntime()
	return s, nil
}
func id() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}
func marshal(v any) string { b, _ := json.Marshal(v); return string(b) }
func digest(v any) string {
	var normalized any
	_ = json.Unmarshal([]byte(marshal(v)), &normalized)
	sum := sha256.Sum256([]byte(marshal(normalized)))
	return hex.EncodeToString(sum[:])
}
func has(values []string, value string) bool {
	for _, v := range values {
		if v == value {
			return true
		}
	}
	return false
}
func (s *Service) Settings() Settings {
	s.mu.Lock()
	defer s.mu.Unlock()
	cfg := cloneSettings(s.cfg)
	cfg.HasSecret = s.key != ""
	return cfg
}
func cloneSettings(cfg Settings) Settings {
	cfg.NodeIDs = append([]string{}, cfg.NodeIDs...)
	cfg.AutoEvents = append([]string{}, cfg.AutoEvents...)
	cfg.Models = append([]string{}, cfg.Models...)
	return cfg
}
func (s *Service) SaveSettings(ctx context.Context, in SettingsInput, actor Actor) (Settings, error) {
	if in.Protocol != ProtocolResponses {
		return Settings{}, fmt.Errorf("%w: only Responses protocol is supported", ErrInvalid)
	}
	if in.MaxIterations == 0 {
		in.MaxIterations = 40
	}
	if in.MaxOperations == 0 {
		in.MaxOperations = 20
	}
	if in.MaxIterations < 1 || in.MaxIterations > 256 || in.MaxOperations < 1 || in.MaxOperations > 100 {
		return Settings{}, ErrInvalid
	}
	in.Endpoint = strings.TrimRight(strings.TrimSpace(in.Endpoint), "/")
	if err := validateEndpoint(in.Endpoint, in.AllowInsecure); err != nil {
		return Settings{}, err
	}
	models, err := normalizeModels(in.Models, in.Model)
	if err != nil {
		return Settings{}, err
	}
	in.Models = models
	in.Model = strings.TrimSpace(in.Model)
	if in.Model == "" && len(models) > 0 {
		in.Model = models[0]
	}
	if in.Model != "" && !has(models, in.Model) || len(in.APIKey) > 8192 || strings.ContainsAny(in.APIKey, "\r\n") {
		return Settings{}, ErrInvalid
	}
	if in.MaxConcurrent < 1 || in.MaxConcurrent > 8 || in.MaxToolCalls < 1 || in.MaxToolCalls > 32 || in.DailyAutoLimit < 1 || in.DailyAutoLimit > 1000 || in.LogLines < 1 || in.LogLines > 500 || in.LogBytes < 1024 || in.LogBytes > 64<<10 || in.ApprovalMinutes < 1 || in.ApprovalMinutes > 60 {
		return Settings{}, ErrInvalid
	}
	if in.Enabled && (len(in.NodeIDs) == 0 || strings.TrimSpace(in.Model) == "") {
		return Settings{}, ErrInvalid
	}
	for _, nodeID := range in.NodeIDs {
		var n database.Node
		if s.db.WithContext(ctx).First(&n, "id = ?", nodeID).Error != nil {
			return Settings{}, ErrScope
		}
	}
	for _, kind := range in.AutoEvents {
		if !has([]string{"container.exited", "container.oom", "container.unhealthy", "container.restart_loop", "node.offline", "task.failed", "image.check_failed", "cd.completed", "cleanup.completed"}, kind) {
			return Settings{}, ErrInvalid
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopped || in.Version != s.cfg.Version {
		return Settings{}, ErrConflict
	}
	cfg := cloneSettings(in.Settings)
	cfg.Version++
	cfg.AuthorizedBy = actor.UserID
	cfg.HasSecret = false
	key := s.key
	if in.APIKey != "" {
		key = in.APIKey
	}
	// A tool capability claim from the browser is never trusted.
	cfg.ToolCapable = s.cfg.ToolCapable
	cfg.TestedFingerprint = s.cfg.TestedFingerprint
	if connectionHash(cfg, key) != connectionHash(s.cfg, s.key) {
		cfg.ToolCapable = false
		cfg.TestedFingerprint = ""
	}
	cipher, err := s.secrets.Encrypt(key)
	if err != nil {
		return Settings{}, err
	}
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for _, row := range []database.Setting{{Key: "internal.ai.settings", Value: marshal(cfg)}, {Key: "internal.ai.key", Value: hex.EncodeToString(cipher)}} {
			if err := tx.Clauses(clause.OnConflict{UpdateAll: true}).Create(&row).Error; err != nil {
				return err
			}
		}
		if !cfg.Enabled || workflowConfigHash(cfg, key) != workflowConfigHash(s.cfg, s.key) {
			return tx.Model(&database.AIOperation{}).Where("status IN ?", []string{"awaiting_approval", "queued"}).Updates(map[string]any{"status": "invalidated", "result": "AI disabled"}).Error
		}
		return tx.Model(&database.AIOperation{}).Where("status IN ? AND node_id NOT IN ?", []string{"awaiting_approval", "queued"}, cfg.NodeIDs).Updates(map[string]any{"status": "invalidated", "result": "Node authorization removed"}).Error
	})
	if err != nil {
		return Settings{}, err
	}
	s.cfg = cfg
	s.key = key
	var runs []database.AIRun
	s.db.Where("status = ?", "running").Find(&runs)
	for _, run := range runs {
		if !cfg.Enabled || workflowConfigHash(cfg, key) != run.ConfigHash {
			if cancel := s.cancels[run.ID]; cancel != nil {
				cancel()
			}
		}
	}
	var rows []database.AIOperation
	s.db.Where("status = ?", "running").Find(&rows)
	for _, row := range rows {
		if !cfg.Enabled || !has(cfg.NodeIDs, row.NodeID) {
			s.tasks.Cancel(row.TaskID)
		}
	}
	cfg.HasSecret = key != ""
	return cloneSettings(cfg), nil
}
func connectionHash(cfg Settings, key string) string {
	return digest([]any{cfg.Protocol, cfg.Endpoint, cfg.Model, cfg.AllowPrivate, cfg.AllowInsecure, key})
}
func (s *Service) TestModel(ctx context.Context) (map[string]any, error) {
	started := time.Now()
	s.mu.Lock()
	cfg, key := s.cfg, s.key
	s.mu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	safeDetail := func(value string) string {
		if key != "" {
			value = strings.ReplaceAll(value, key, "[redacted]")
		}
		return redact.Bounded(value, 1024)
	}
	first, err := s.deps.Model.Complete(ctx, cfg, key, []ModelMessage{{Role: "user", Text: "Reply with a short connection confirmation."}}, nil)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(first.Text) == "" {
		return nil, errors.New("model returned no text")
	}
	failure, toolErr := s.probeTools(ctx, cfg, key)
	capable := failure == ""
	s.mu.Lock()
	defer s.mu.Unlock()
	if connectionHash(s.cfg, s.key) != connectionHash(cfg, key) {
		return nil, ErrConflict
	}
	s.cfg.ToolCapable = capable
	s.cfg.TestedFingerprint = connectionHash(cfg, key)
	row := database.Setting{Key: "internal.ai.settings", Value: marshal(s.cfg)}
	if err := s.db.WithContext(ctx).Clauses(clause.OnConflict{UpdateAll: true}).Create(&row).Error; err != nil {
		return nil, err
	}
	result := map[string]any{
		"text": true, "tool_capable": capable, "summary_only": !capable,
		"model": cfg.Model, "duration_ms": time.Since(started).Milliseconds(),
		"text_response": safeDetail(first.Text),
	}
	if failure != "" {
		result["tool_failure"] = failure
	}
	if toolErr != nil {
		result["tool_error"] = safeDetail(toolErr.Error())
	}
	return result, nil
}
func (s *Service) probeTools(ctx context.Context, cfg Settings, key string) (string, error) {
	// Use a required argument like the real tools. Some compatible models copy
	// schema keywords into arguments when the probe has no parameters.
	const probeMessage = "suma_connection_test"
	probe := Tool{
		Name: "connection_probe", Description: "Call this tool with the required message to validate tool support",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{"message": map[string]any{
				"type": "string", "enum": []string{probeMessage},
			}},
			"required": []string{"message"}, "additionalProperties": false,
		},
	}
	second, toolErr := s.deps.Model.Complete(ctx, cfg, key, []ModelMessage{{Role: "user", Text: fmt.Sprintf("Call connection_probe exactly once with {\"message\":%q}.", probeMessage)}}, []Tool{probe})
	var probeArgs struct {
		Message string `json:"message"`
	}
	failure := ""
	switch {
	case toolErr != nil:
		failure = "request_failed"
	case len(second.Calls) == 0:
		failure = "not_called"
	case len(second.Calls) != 1 || second.Calls[0].Name != probe.Name || strings.TrimSpace(second.Calls[0].ID) == "":
		failure = "unexpected_call"
	case strict(second.Calls[0].Arguments, &probeArgs) != nil || probeArgs.Message != probeMessage:
		failure = "invalid_arguments"
	}
	return failure, toolErr
}

func (s *Service) validateActor(ctx context.Context, a Actor) error {
	if a.Source == "chat" && (a.BindingID == "" || a.ExternalUserID == "" || a.ChatID == "") {
		return ErrScope
	}
	if a.UserID == 0 {
		return ErrScope
	}
	var u database.User
	if err := s.db.WithContext(ctx).First(&u, a.UserID).Error; err != nil {
		return ErrScope
	}
	if s.deps.ActorValid != nil {
		return s.deps.ActorValid(ctx, a)
	}
	if a.BindingID != "" {
		return ErrScope
	}
	return nil
}
func (s *Service) Start(ctx context.Context, in RunInput, a Actor) (Run, error) {
	if in.ConversationID == "" {
		c, err := s.CreateConversation(ctx, ConversationInput{}, a)
		if err != nil {
			return Run{}, err
		}
		in.ConversationID = c.ID
	}
	nodes := []string{}
	if in.NodeID != "" {
		nodes = []string{in.NodeID}
	}
	return s.postMessage(ctx, in.ConversationID, MessageInput{Question: in.Question, Model: in.Model, RequestID: id(), Context: TargetContext{NodeIDs: nodes, ResourceNodeID: in.ResourceNodeID, ResourceKind: in.ResourceType, ResourceID: in.ResourceID}}, in.EventID, a)
}

func strict(raw []byte, out any) error {
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(out); err != nil {
		return ErrInvalid
	}
	var extra any
	if dec.Decode(&extra) != io.EOF {
		return ErrInvalid
	}
	return nil
}
func (s *Service) audit(ctx context.Context, db *gorm.DB, a Actor, runID, opID, action, resource, result string) error {
	return s.auditNode(ctx, db, a, runID, opID, action, resource, result, "")
}
func (s *Service) auditNode(ctx context.Context, db *gorm.DB, a Actor, runID, opID, action, resource, result, nodeID string) error {
	return s.deps.Audit.RecordAI(ctx, db, database.AIAudit{NodeID: nodeID, RunID: runID, OperationID: opID, UserID: a.UserID, Source: a.Source, BindingID: a.BindingID, ExternalUserID: a.ExternalUserID, ChatID: a.ChatID, IP: a.IP, Action: action, Resource: resource, Result: redact.Bounded(result, 1024)})
}
func decodeOperation(row database.AIOperation) Operation {
	op := Operation{AIOperation: row, Parameters: json.RawMessage(row.ParametersJSON), ReviewToken: row.SnapshotHash}
	_ = json.Unmarshal([]byte(row.SnapshotJSON), &op.Snapshot)
	op.Snapshot.RuntimeKey = ""
	op.Confirmations = []Confirmation{}
	_ = json.Unmarshal([]byte(row.ConfirmationsJSON), &op.Confirmations)
	if op.Confirmations == nil {
		op.Confirmations = []Confirmation{}
	}
	op.Verification = json.RawMessage(row.VerificationJSON)
	if len(op.Verification) == 0 {
		op.Verification = json.RawMessage(`{}`)
	}
	return op
}
func (s *Service) Operations(ctx context.Context) ([]Operation, error) {
	s.Expire(ctx)
	var rows []database.AIOperation
	err := s.db.WithContext(ctx).Order("created_at DESC").Limit(200).Find(&rows).Error
	out := []Operation{}
	for _, row := range rows {
		out = append(out, decodeOperation(row))
	}
	return out, err
}
func (s *Service) Operation(ctx context.Context, id string) (Operation, error) {
	s.Expire(ctx)
	var row database.AIOperation
	err := s.db.WithContext(ctx).First(&row, "id = ?", id).Error
	return decodeOperation(row), err
}
func (s *Service) Runs(ctx context.Context) ([]Run, error) {
	var rows []database.AIRun
	err := s.db.WithContext(ctx).Order("created_at DESC").Limit(200).Find(&rows).Error
	out := []Run{}
	for _, row := range rows {
		out = append(out, decodeRun(row))
	}
	return out, err
}
func (s *Service) Run(ctx context.Context, key string) (Run, error) {
	var row database.AIRun
	if err := s.db.WithContext(ctx).First(&row, "id = ?", key).Error; err != nil {
		return Run{}, err
	}
	out := decodeRun(row)
	var steps []database.AIPlanStep
	if err := s.db.WithContext(ctx).Where("run_id = ?", key).Order("position ASC").Find(&steps).Error; err != nil {
		return out, err
	}
	for _, step := range steps {
		view := PlanStep{AIPlanStep: step, Parameters: json.RawMessage(step.ParametersJSON), DependsOn: []int{}}
		_ = json.Unmarshal([]byte(step.DependsJSON), &view.DependsOn)
		out.Steps = append(out.Steps, view)
	}
	var input database.AIInteraction
	err := s.db.WithContext(ctx).Where("run_id = ? AND status = ?", key, "pending").Order("created_at DESC").First(&input).Error
	if err == nil {
		out.Interaction = &Interaction{AIInteraction: input, Options: []ResourceOption{}}
		_ = json.Unmarshal([]byte(input.OptionsJSON), &out.Interaction.Options)
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return out, err
	}
	return out, nil
}
func (s *Service) Audits(ctx context.Context) ([]database.AIAudit, error) {
	return s.deps.Audit.ListAI(ctx, 500)
}
func (s *Service) Decide(ctx context.Context, key string, in Decision, a Actor) (Operation, error) {
	if err := s.validateActor(ctx, a); err != nil {
		return Operation{}, err
	}
	if !requestPattern.MatchString(in.RequestID) {
		return Operation{}, ErrInvalid
	}
	var row database.AIOperation
	if err := s.db.WithContext(ctx).First(&row, "id = ?", key).Error; err != nil {
		return Operation{}, err
	}
	decisionHash := digest([]any{in, a.UserID, a.Source, a.BindingID, a.ChatID})
	if row.DecisionKey == in.RequestID {
		if row.DecisionHash != decisionHash {
			return Operation{}, ErrConflict
		}
		return decodeOperation(row), nil
	}
	if row.Status != "awaiting_approval" || row.SnapshotHash != in.ReviewToken || !s.deps.Now().Before(row.ExpiresAt) {
		return Operation{}, ErrConflict
	}
	var run database.AIRun
	if err := s.db.WithContext(ctx).First(&run, "id = ?", row.RunID).Error; err != nil {
		return Operation{}, err
	}
	s.mu.Lock()
	cfg, modelKey := cloneSettings(s.cfg), s.key
	s.mu.Unlock()
	if !cfg.Enabled || !has(cfg.NodeIDs, row.NodeID) || workflowConfigHash(cfg, modelKey) != run.ConfigHash {
		return Operation{}, ErrScope
	}
	if err := s.runActorValid(ctx, run); err != nil {
		return Operation{}, err
	}
	if in.Approve {
		if err := s.checkpointReady(ctx, run); err != nil {
			return Operation{}, err
		}
	}
	var requirements []Confirmation
	_ = json.Unmarshal([]byte(row.ConfirmationsJSON), &requirements)
	if in.Approve {
		for _, required := range requirements {
			expected := required.Expected
			if required.Checkbox {
				expected = "true"
			}
			if in.Confirmations[required.Key] != expected {
				return Operation{}, fmt.Errorf("%w: confirmation required: %s", ErrInvalid, required.Label)
			}
		}
		req := operationRequest(row)
		current, err := s.deps.Freeze(ctx, row.NodeID, req)
		if err != nil || operationDigest(req, current) != row.SnapshotHash {
			_ = s.db.WithContext(ctx).Model(&database.AIOperation{}).Where("id = ? AND status = ?", key, "awaiting_approval").Updates(map[string]any{"status": "invalidated", "result": "Target, runtime or configuration changed"}).Error
			return Operation{}, ErrConflict
		}
	}
	var prepared database.Task
	launched := false
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := s.lockConversation(ctx, tx, run.ConversationID); err != nil {
			return err
		}
		var live database.AIRun
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&live, "id = ?", row.RunID).Error; err != nil {
			return err
		}
		var locked database.AIOperation
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&locked, "id = ?", key).Error; err != nil {
			return err
		}
		if locked.DecisionKey == in.RequestID {
			if locked.DecisionHash != decisionHash {
				return ErrConflict
			}
			return nil
		}
		if locked.Status != "awaiting_approval" || locked.SnapshotHash != in.ReviewToken || !s.deps.Now().Before(locked.ExpiresAt) || live.Status != "waiting_approval" {
			return ErrConflict
		}
		status, runStatus := "rejected", "paused"
		if in.Approve {
			status, runStatus = "queued", "waiting_task"
			var err error
			prepared, err = s.tasks.Prepare(tx, row.NodeID, "ai."+row.Action, row.Title)
			if err != nil {
				return err
			}
			launched = true
		}
		if err := tx.Model(&locked).Updates(map[string]any{"status": status, "approved_by": a.UserID, "approval_source": actorSource(a), "binding_id": a.BindingID, "external_user_id": a.ExternalUserID, "chat_id": a.ChatID, "task_id": prepared.ID, "decision_key": in.RequestID, "decision_hash": decisionHash, "confirmed_json": marshal(in.Confirmations)}).Error; err != nil {
			return err
		}
		if err := tx.Model(&live).Updates(map[string]any{"status": runStatus, "phase": "execute", "task_progress": 0, "task_status": "pending"}).Error; err != nil {
			return err
		}
		if err := tx.Model(&database.AIPlanStep{}).Where("id = ?", locked.StepID).Update("status", status).Error; err != nil {
			return err
		}
		if err := tx.Model(&database.AIInteraction{}).Where("run_id = ? AND kind = ? AND status = ?", row.RunID, "approval", "pending").Update("status", status).Error; err != nil {
			return err
		}
		if err := s.audit(ctx, tx, a, row.RunID, key, status, row.ResourceID, status); err != nil {
			return err
		}
		return s.eventTx(ctx, tx, live.ConversationID, live.ID, "operation."+status, map[string]any{"operation_id": key, "task_id": prepared.ID})
	})
	if err != nil {
		return Operation{}, err
	}
	if launched {
		s.tasks.Launch(prepared, func(ctx context.Context, report task.Reporter) error { return s.execute(ctx, key, a, report) })
	}
	s.signalRuntime()
	return s.Operation(ctx, key)
}
func operationRequest(row database.AIOperation) OperationRequest {
	return OperationRequest{StepID: row.StepID, NodeID: row.NodeID, Action: row.Action, ResourceID: row.ResourceID, Parameters: json.RawMessage(row.ParametersJSON)}
}
func (s *Service) execute(ctx context.Context, key string, a Actor, report task.Reporter) error {
	var row database.AIOperation
	if err := s.db.First(&row, "id = ?", key).Error; err != nil {
		return err
	}
	if err := s.validateActor(ctx, a); err != nil {
		s.finish(row, "invalidated", err, Verification{})
		return err
	}
	var run database.AIRun
	if err := s.db.First(&run, "id = ?", row.RunID).Error; err != nil {
		return err
	}
	s.mu.Lock()
	cfg, modelKey := cloneSettings(s.cfg), s.key
	stopped := s.stopped
	s.mu.Unlock()
	if stopped || !cfg.Enabled || !has(cfg.NodeIDs, row.NodeID) || run.Status != "waiting_task" || workflowConfigHash(cfg, modelKey) != run.ConfigHash {
		s.finish(row, "invalidated", ErrScope, Verification{})
		return ErrScope
	}
	if err := s.runActorValid(ctx, run); err != nil {
		s.finish(row, "invalidated", err, Verification{})
		return err
	}
	if err := s.checkpointReady(ctx, run); err != nil {
		s.finish(row, "invalidated", err, Verification{})
		return err
	}
	req := operationRequest(row)
	snap, err := s.deps.Freeze(ctx, row.NodeID, req)
	if err != nil || operationDigest(req, snap) != row.SnapshotHash {
		s.finish(row, "invalidated", ErrConflict, Verification{})
		return ErrConflict
	}
	claimed := s.db.Model(&database.AIOperation{}).Where("id = ? AND status = ?", key, "queued").Update("status", "running")
	if claimed.Error != nil {
		return claimed.Error
	}
	if claimed.RowsAffected != 1 {
		return ErrConflict
	}
	_ = s.db.Model(&database.AIPlanStep{}).Where("id = ?", row.StepID).Update("status", "running").Error
	if ctx.Err() != nil {
		err = ctx.Err()
	} else if err = s.validateActor(ctx, a); err == nil {
		if err = s.runActorValid(ctx, run); err != nil {
			s.finish(row, "invalidated", err, Verification{})
			return err
		}
		if s.deps.Execute == nil {
			err = ErrInvalid
		} else {
			err = s.deps.Execute(ctx, row, snap, report)
		}
	}
	verification := Verification{Evidence: []Evidence{}}
	if err == nil {
		if s.deps.Verify == nil {
			err = errors.New("execution completed but result verification is unavailable")
		} else {
			verification, err = s.deps.Verify(ctx, row, snap)
			if err == nil && !verification.Satisfied {
				err = errors.New("execution completed but expected state was not verified: " + verification.Summary)
			}
		}
	}
	status := "completed"
	if err != nil {
		status = "failed"
	}
	if ctx.Err() != nil {
		status = "interrupted"
	}
	s.finish(row, status, err, verification)
	return err
}
func (s *Service) finish(row database.AIOperation, status string, executionErr error, verification Verification) {
	result := "Execution and verification completed"
	if executionErr != nil {
		result = s.cleanText(executionErr.Error(), 2048)
	}
	verification.Summary = s.cleanText(verification.Summary, 2048)
	for i := range verification.Evidence {
		verification.Evidence[i].Content = s.cleanText(verification.Evidence[i].Content, 64<<10)
	}
	a := Actor{Source: row.ApprovalSource, BindingID: row.BindingID, ExternalUserID: row.ExternalUserID, ChatID: row.ChatID}
	if row.ApprovedBy != nil {
		a.UserID = *row.ApprovedBy
	}
	_ = s.db.Transaction(func(tx *gorm.DB) error {
		var run database.AIRun
		if err := tx.First(&run, "id = ?", row.RunID).Error; err != nil {
			return err
		}
		if err := s.lockConversation(context.Background(), tx, run.ConversationID); err != nil {
			return err
		}
		if err := tx.Model(&database.AIOperation{}).Where("id = ?", row.ID).Updates(map[string]any{"status": status, "result": result, "verification_json": marshal(verification)}).Error; err != nil {
			return err
		}
		if err := tx.Model(&database.AIPlanStep{}).Where("id = ?", row.StepID).Update("status", status).Error; err != nil {
			return err
		}
		if err := s.audit(context.Background(), tx, a, row.RunID, row.ID, "execution", row.ResourceID, status+": "+result); err != nil {
			return err
		}
		return s.eventTx(context.Background(), tx, run.ConversationID, run.ID, "operation."+status, map[string]any{"operation_id": row.ID, "task_id": row.TaskID, "verification": verification})
	})
	severity := "info"
	if executionErr != nil {
		severity = "error"
	}
	s.emit(event.Event{Type: "ai.completed", Severity: severity, NodeID: row.NodeID, RunID: row.RunID, OperationID: row.ID, TaskID: row.TaskID, Title: row.Title, Message: status + ": " + result})
	s.signalRuntime()
}
func (s *Service) Expire(ctx context.Context) {
	var rows []database.AIOperation
	s.db.WithContext(ctx).Where("status = ? AND expires_at <= ?", "awaiting_approval", s.deps.Now()).Find(&rows)
	for _, row := range rows {
		changed := false
		err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			r := tx.Model(&database.AIOperation{}).Where("id = ? AND status = ?", row.ID, "awaiting_approval").Update("status", "expired")
			if r.Error != nil {
				return r.Error
			}
			changed = r.RowsAffected == 1
			if !changed {
				return nil
			}
			return s.audit(ctx, tx, Actor{UserID: row.RequestedBy, Source: "system"}, row.RunID, row.ID, "expiry", row.ResourceID, "expired")
		})
		if err == nil && changed {
			s.emit(event.Event{Type: "ai.expired", Severity: "warning", NodeID: row.NodeID, RunID: row.RunID, OperationID: row.ID, Title: row.Title, Message: "Approval expired; a new proposal is required"})
		}
	}
}
func (s *Service) OnEvent(e event.Event) {
	cfg := s.Settings()
	if !cfg.Enabled || !has(cfg.AutoEvents, e.Type) || !has(cfg.NodeIDs, e.NodeID) {
		return
	}
	_, err := s.Start(context.Background(), RunInput{NodeID: e.NodeID, Question: "Analyze event " + e.Type + " on " + e.ResourceID + ". Explain causes, missing evidence, and suggested actions.", EventID: e.ID, ResourceType: e.ResourceType, ResourceID: e.ResourceID}, Actor{UserID: cfg.AuthorizedBy, Source: "auto"})
	if errors.Is(err, ErrBudget) {
		s.emit(event.Event{Type: "ai.budget", Severity: "warning", Title: "AI automatic diagnosis daily limit reached", DedupeKey: "ai:budget:" + s.deps.Now().Format("2006-01-02")})
	}
}
func (s *Service) emit(e event.Event) {
	if s.deps.Emit != nil {
		s.deps.Emit(e)
	}
}
func (s *Service) Stop() {
	s.mu.Lock()
	s.stopped = true
	if s.runtimeCancel != nil {
		s.runtimeCancel()
	}
	for _, cancel := range s.cancels {
		cancel()
	}
	var rows []database.AIOperation
	s.db.Where("status IN ?", []string{"queued", "running"}).Find(&rows)
	for _, row := range rows {
		s.tasks.Cancel(row.TaskID)
	}
	s.mu.Unlock()
	s.wg.Wait()
	var launched []database.AIOperation
	s.db.Where("task_id <> ''").Find(&launched)
	waitCtx, waitCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer waitCancel()
	for _, operation := range launched {
		_ = s.tasks.Wait(waitCtx, operation.TaskID)
	}
}

// Recovery reads actual state once and records it; it never replays a change.
func (s *Service) ReconcileInterrupted(ctx context.Context) {
	var rows []database.AIOperation
	if s.db.WithContext(ctx).Where("status = ? AND result LIKE ?", "interrupted", "SUMA restarted;%").Order("created_at ASC").Limit(200).Find(&rows).Error != nil {
		return
	}
	for _, row := range rows {
		if ctx.Err() != nil {
			return
		}
		cfg := s.Settings()
		result := "Recovery state check skipped: AI is disabled or this node is no longer authorized. Inspect actual state before a new proposal."
		if cfg.Enabled && has(cfg.NodeIDs, row.NodeID) && s.deps.Freeze != nil {
			checkCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
			req := OperationRequest{Action: row.Action, ResourceID: row.ResourceID, Parameters: json.RawMessage(row.ParametersJSON)}
			current, err := s.deps.Freeze(checkCtx, row.NodeID, req)
			cancel()
			if err != nil {
				result = "Recovery state unavailable: " + redact.Bounded(err.Error(), 1024)
			} else {
				result = "Recovery state collected at " + s.deps.Now().Format(time.RFC3339) + ": " + redact.Bounded(string(current.Details), 8192) + ". No operation was replayed; another change needs a new approval."
			}
		}
		if s.db.WithContext(ctx).Model(&database.AIOperation{}).Where("id = ? AND status = ?", row.ID, "interrupted").Update("result", result).Error == nil {
			s.audit(ctx, s.db, Actor{UserID: row.RequestedBy, Source: "recovery"}, row.RunID, row.ID, "restart_reconcile", row.ResourceID, "interrupted")
			s.emit(event.Event{Type: "ai.completed", Severity: "warning", NodeID: row.NodeID, RunID: row.RunID, OperationID: row.ID, TaskID: row.TaskID, Title: row.Title, Message: result})
		}
	}
}
func (s *Service) PreviewText(op Operation) string {
	return fmt.Sprintf("%s\nOperation: %s\nNode: %s\nResource: %s\nAction: %s\nParameters: %s\nImpact: %s\nCurrent state: %s\nExpires: %s\nEach action needs separate approval; changed targets invalidate this preview.", op.Title, op.ID, op.NodeID, op.ResourceID, op.Action, string(op.Parameters), op.Impact, string(op.Snapshot.Details), op.ExpiresAt.Format(time.RFC3339))
}

func operationDigest(req OperationRequest, snap Snapshot) string {
	req.StepID = ""
	return digest([]any{req, snap})
}
