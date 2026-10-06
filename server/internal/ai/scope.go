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

// Every Docker read and proposal resolves one explicit runtime, even when its
// conversation spans the whole authorized fleet.
func resolveToolNode(scope, requested string, authorized []string) (string, error) {
	if requested == "" {
		requested = scope
	}
	if requested == "" || scope != "" && requested != scope || !has(authorized, requested) {
		return "", ErrScope
	}
	return requested, nil
}

func globalTools() []Tool {
	definitions := tools()
	for i := range definitions {
		schema := map[string]any{}
		for key, value := range definitions[i].Parameters {
			schema[key] = value
		}
		properties := map[string]any{}
		for key, value := range schema["properties"].(map[string]any) {
			properties[key] = value
		}
		schema["properties"] = properties
		definitions[i].Parameters = schema
		schema["properties"].(map[string]any)["node_id"] = map[string]any{"type": "string"}
		schema["required"] = append(schema["required"].([]string), "node_id")
		definitions[i].Description += " Supply the explicit node_id from the authorized node directory."
	}
	return definitions
}
