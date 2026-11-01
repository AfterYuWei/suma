package deployment

import (
	"encoding/json"
	"strings"
)

func RuntimeIsHealthy(value string) bool {
	type service struct {
		State  string `json:"State"`
		Health string `json:"Health"`
	}
	rows := []service{}
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return false
	}
	if strings.HasPrefix(trimmed, "[") {
		if json.Unmarshal([]byte(trimmed), &rows) != nil {
			return false
		}
	} else {
		for _, line := range strings.Split(trimmed, "\n") {
			var row service
			if json.Unmarshal([]byte(line), &row) != nil {
				return false
			}
			rows = append(rows, row)
		}
	}
	if len(rows) == 0 {
		return false
	}
	for _, row := range rows {
		if !strings.EqualFold(row.State, "running") || (row.Health != "" && !strings.EqualFold(row.Health, "healthy")) {
			return false
		}
	}
	return true
}
