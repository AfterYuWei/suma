package ai

import (
	"encoding/json"

	"github.com/suma/suma/server/internal/database"
)

func authorizedHistory(run database.AIRun, authorized []string) bool {
	if run.NodeID != "" {
		return has(authorized, run.NodeID)
	}
	var nodes []string
	if json.Unmarshal([]byte(run.NodeIDsJSON), &nodes) != nil || len(nodes) == 0 {
		return false
	}
	for _, node := range nodes {
		if !has(authorized, node) {
			return false
		}
	}
	return true
}
