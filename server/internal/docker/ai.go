package docker

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

// AIContainerState includes runtime identity and configuration without environment
// values or commands. Inspection is always performed on the explicit node.
func (a *Adapter) AIContainerState(ctx context.Context, id string) (map[string]any, error) {
	row, err := a.client.ContainerInspect(ctx, id)
	if err != nil {
		return nil, err
	}
	if row.State == nil {
		return nil, fmt.Errorf("container state unavailable")
	}
	raw, _ := json.Marshal([]any{row.Config, row.HostConfig})
	sum := sha256.Sum256(raw)
	state := map[string]any{"config_fingerprint": hex.EncodeToString(sum[:]), "id": row.ID, "image_id": row.Image, "created": row.Created, "status": row.State.Status, "pid": row.State.Pid, "started_at": row.State.StartedAt, "finished_at": row.State.FinishedAt, "restart_count": row.RestartCount, "oom_killed": row.State.OOMKilled, "exit_code": row.State.ExitCode}
	if row.State.Health != nil {
		state["health"] = map[string]any{"status": row.State.Health.Status, "failing_streak": row.State.Health.FailingStreak}
	}
	return state, nil
}
