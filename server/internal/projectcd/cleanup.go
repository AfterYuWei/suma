package projectcd

import (
	"context"
	"encoding/json"
	"github.com/suma/suma/server/internal/compose"
	"github.com/suma/suma/server/internal/database"
)

// Retained snapshots protect resources required for rollback even after detach.
func (s *Service) CleanupReferences(ctx context.Context, node string) (compose.CleanupReferences, error) {
	result := compose.CleanupReferences{}
	var records []database.ProjectCDRecord
	if err := s.db.WithContext(ctx).Where("project_cd_id IN (SELECT id FROM project_cds WHERE node_id = ?) AND snapshot_ciphertext IS NOT NULL", node).Find(&records).Error; err != nil {
		return result, err
	}
	for _, record := range records {
		value, err := s.decode(record)
		if err != nil {
			return result, err
		}
		var document struct {
			Services map[string]struct{ Image string }
			Networks map[string]struct{ Name string }
			Volumes  map[string]struct{ Name string }
		}
		if err := json.Unmarshal([]byte(value.Rendered), &document); err != nil {
			return result, err
		}
		for _, service := range document.Services {
			if service.Image != "" {
				result.Images = append(result.Images, service.Image)
			}
		}
		for key, network := range document.Networks {
			name := network.Name
			if name == "" {
				name = key
			}
			result.Networks = append(result.Networks, name)
		}
		for key, volume := range document.Volumes {
			name := volume.Name
			if name == "" {
				name = key
			}
			result.Volumes = append(result.Volumes, name)
		}
	}
	return result, nil
}
