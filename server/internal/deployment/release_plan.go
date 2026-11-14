package deployment

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/suma/suma/server/internal/database"
	"github.com/suma/suma/server/internal/secret"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var ErrPlanChanged = errors.New("release plan expired or configuration changed; prepare and review again")
var ErrPlanBusy = errors.New("another delivery operation is active")

type PrepareReleaseInput struct {
	Operation      string `json:"operation"`
	ReleaseID      uint   `json:"release_id"`
	SourceRecordID uint   `json:"source_record_id"`
}

type ConfirmReleaseInput struct {
	NodeIDs       []string `json:"node_ids"`
	ForceRecreate bool     `json:"force_recreate"`
}

type ReleasePlanView struct {
	Warnings          []string          `json:"warnings,omitempty"`
	ID                string            `json:"id"`
	ExpiresAt         time.Time         `json:"expires_at"`
	Operation         string            `json:"operation"`
	SourceRecordID    uint              `json:"source_record_id,omitempty"`
	ReleaseID         uint              `json:"release_id,omitempty"`
	CommitSHA         string            `json:"commit_sha"`
	CommitMessage     string            `json:"commit_message"`
	ComposeFiles      []string          `json:"compose_files"`
	Nodes             []ReleaseNodeView `json:"nodes"`
	ImagesPinned      bool              `json:"images_pinned"`
	AutoRollback      bool              `json:"auto_rollback"`
	DeploymentTimeout int               `json:"deployment_timeout"`
}

type ReleaseNodeView struct {
	TargetCommit     string           `json:"target_commit"`
	ComposeBefore    string           `json:"compose_before"`
	ComposeAfter     string           `json:"compose_after"`
	NodeID           string           `json:"node_id"`
	NodeName         string           `json:"node_name"`
	PreviousRecordID *uint            `json:"previous_record_id,omitempty"`
	PreviousCommit   string           `json:"previous_commit"`
	Changed          bool             `json:"changed"`
	RuntimeHealthy   bool             `json:"runtime_healthy"`
	HealthChecked    bool             `json:"health_checked"`
	CanRestore       bool             `json:"can_restore"`
	Images           []ImageChange    `json:"images"`
	Services         []ServiceChange  `json:"services"`
	Environment      []VariableChange `json:"environment"`
}
type ImageChange struct {
	Service   string `json:"service"`
	Declared  string `json:"declared"`
	Previous  string `json:"previous"`
	Reference string `json:"reference"`
	Platform  string `json:"platform"`
	Changed   bool   `json:"changed"`
}
type ServiceChange struct {
	Name   string `json:"name"`
	Action string `json:"action"`
}
type VariableChange struct {
	Name   string `json:"name"`
	Action string `json:"action"`
}

func SaveReleasePlan(ctx context.Context, db *gorm.DB, secrets *secret.Store, kind string, owner uint, version, taskID string, payload any, view ReleasePlanView) (ReleasePlanView, error) {
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return view, err
	}
	view.ID = hex.EncodeToString(id[:])
	view.ExpiresAt = time.Now().UTC().Add(10 * time.Minute)
	raw, err := json.Marshal(payload)
	if err != nil {
		return view, err
	}
	cipher, err := secrets.Encrypt(string(raw))
	if err != nil {
		return view, err
	}
	preview, err := json.Marshal(view)
	if err != nil {
		return view, err
	}
	row := database.ReleasePlan{ID: view.ID, Kind: kind, OwnerID: owner, Version: version, PayloadCiphertext: cipher, PreviewJSON: string(preview), PreparationTaskID: taskID, ExpiresAt: view.ExpiresAt, CreatedAt: time.Now().UTC()}
	return view, db.WithContext(ctx).Create(&row).Error
}

func LoadReleasePlan(ctx context.Context, db *gorm.DB, secrets *secret.Store, kind string, owner uint, id string, payload any) (database.ReleasePlan, ReleasePlanView, error) {
	var row database.ReleasePlan
	var view ReleasePlanView
	if len(id) != 32 {
		return row, view, gorm.ErrRecordNotFound
	}
	if err := db.WithContext(ctx).Where("id = ? AND kind = ? AND owner_id = ?", id, kind, owner).First(&row).Error; err != nil {
		return row, view, err
	}
	if err := json.Unmarshal([]byte(row.PreviewJSON), &view); err != nil {
		return row, view, err
	}
	if payload != nil {
		raw, err := secrets.Decrypt(row.PayloadCiphertext)
		if err != nil {
			return row, view, err
		}
		if err := json.Unmarshal([]byte(raw), payload); err != nil {
			return row, view, err
		}
	}
	return row, view, nil
}

// ClaimReleasePlan runs only inside Task creation. Interrupted Tasks retain the
// claim and are never replayed by a repeated confirmation request.
func ClaimReleasePlan(tx *gorm.DB, id, version, taskID string) error {
	var row database.ReleasePlan
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&row, "id = ?", id).Error; err != nil {
		return err
	}
	if row.TaskID != "" || row.Version != version || !time.Now().Before(row.ExpiresAt) {
		return ErrPlanChanged
	}
	var preparation database.Task
	if err := tx.First(&preparation, "id = ? AND status = ?", row.PreparationTaskID, "success").Error; err != nil {
		return ErrPlanChanged
	}
	return tx.Model(&row).Update("task_id", taskID).Error
}

func SelectedReleaseNodes(view ReleasePlanView, requested []string) ([]string, error) {
	if requested == nil {
		for _, node := range view.Nodes {
			requested = append(requested, node.NodeID)
		}
	}
	if len(requested) == 0 {
		return nil, errors.New("select at least one release target")
	}
	allowed := map[string]bool{}
	for _, node := range view.Nodes {
		allowed[node.NodeID] = true
	}
	seen := map[string]bool{}
	for _, id := range requested {
		if !allowed[id] || seen[id] {
			return nil, errors.New("release targets must be a unique subset of the reviewed plan")
		}
		seen[id] = true
	}
	return requested, nil
}
