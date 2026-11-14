package deployment

import (
	"encoding/json"
	"strings"
)

func RuntimeIsHealthy(value string) bool {
	healthy, _, _ := RuntimeHealth(value)
	return healthy
}

// RuntimeHealth distinguishes a running container from an explicit healthcheck.
func RuntimeHealth(value string) (bool, int, int) {
	type service struct {
		State  string `json:"State"`
		Health string `json:"Health"`
	}
	rows := []service{}
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return false, 0, 0
	}
	if strings.HasPrefix(trimmed, "[") {
		if json.Unmarshal([]byte(trimmed), &rows) != nil {
			return false, 0, 0
		}
	} else {
		for _, line := range strings.Split(trimmed, "\n") {
			var row service
			if json.Unmarshal([]byte(line), &row) != nil {
				return false, 0, 0
			}
			rows = append(rows, row)
		}
	}
	if len(rows) == 0 {
		return false, 0, 0
	}
	checks := 0
	healthy := true
	for _, row := range rows {
		if row.Health != "" {
			checks++
		}
		if !strings.EqualFold(row.State, "running") || (row.Health != "" && !strings.EqualFold(row.Health, "healthy")) {
			healthy = false
		}
	}
	return healthy, checks, len(rows)
}
