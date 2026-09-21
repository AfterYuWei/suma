package node

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/suma/suma/server/internal/database"
	"gorm.io/gorm"
)

type GroupInput struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

type GroupView struct {
	ID          uint      `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	IsDefault   bool      `json:"is_default"`
	NodeCount   int64     `json:"node_count"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

func (s *Service) ListGroups(ctx context.Context) ([]GroupView, error) {
	var rows []database.NodeGroup
	if err := s.db.WithContext(ctx).Order("LOWER(name) ASC, id ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	var memberships []database.NodeGroupNode
	if err := s.db.WithContext(ctx).Find(&memberships).Error; err != nil {
		return nil, err
	}
	counts := make(map[uint]int64, len(rows))
	for _, membership := range memberships {
		counts[membership.GroupID]++
	}
	result := make([]GroupView, 0, len(rows))
	for _, row := range rows {
		result = append(result, groupView(row, counts[row.ID]))
	}
	return result, nil
}

func (s *Service) GetGroup(ctx context.Context, id uint) (GroupView, error) {
	var row database.NodeGroup
	if id == 0 {
		return GroupView{}, gorm.ErrRecordNotFound
	}
	if err := s.db.WithContext(ctx).First(&row, id).Error; err != nil {
		return GroupView{}, err
	}
	var count int64
	if err := s.db.WithContext(ctx).Model(&database.NodeGroupNode{}).Where("group_id = ?", id).Count(&count).Error; err != nil {
		return GroupView{}, err
	}
	return groupView(row, count), nil
}

func (s *Service) CreateGroup(ctx context.Context, input GroupInput) (GroupView, error) {
	name, description, err := validateGroupInput(input)
	if err != nil {
		return GroupView{}, err
	}
	if groupNameExists(s.db.WithContext(ctx), name, 0) {
		return GroupView{}, errors.New("node group name already exists")
	}
	row := database.NodeGroup{Name: name, Description: description}
	if err := s.db.WithContext(ctx).Create(&row).Error; err != nil {
		if groupNameExists(s.db.WithContext(ctx), name, 0) {
			return GroupView{}, errors.New("node group name already exists")
		}
		return GroupView{}, err
	}
	return groupView(row, 0), nil
}

func (s *Service) UpdateGroup(ctx context.Context, id uint, input GroupInput) (GroupView, error) {
	name, description, err := validateGroupInput(input)
	if err != nil {
		return GroupView{}, err
	}
	var row database.NodeGroup
	if err := s.db.WithContext(ctx).First(&row, id).Error; err != nil {
		return GroupView{}, err
	}
	if groupNameExists(s.db.WithContext(ctx), name, id) {
		return GroupView{}, errors.New("node group name already exists")
	}
	if err := s.db.WithContext(ctx).Model(&row).Updates(map[string]any{"name": name, "description": description}).Error; err != nil {
		return GroupView{}, err
	}
	return s.GetGroup(ctx, id)
}

func (s *Service) DeleteGroup(ctx context.Context, id uint) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var row database.NodeGroup
		if err := tx.First(&row, id).Error; err != nil {
			return err
		}
		if err := tx.Where("group_id = ?", id).Delete(&database.NodeGroupNode{}).Error; err != nil {
			return err
		}
		return tx.Delete(&row).Error
	})
}

// ListFiltered returns nodes for an organizational filter. It deliberately
// does not alter or resolve a runtime context.
func (s *Service) ListFiltered(ctx context.Context, groupID *uint) ([]View, error) {
	query := s.db.WithContext(ctx).Model(&database.Node{})
	if groupID != nil {
		if _, err := s.GetGroup(ctx, *groupID); err != nil {
			return nil, err
		}
		query = query.Joins("JOIN node_group_nodes ON node_group_nodes.node_id = nodes.id AND node_group_nodes.group_id = ?", *groupID)
	}
	var rows []database.Node
	if err := query.Order("nodes.name ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	return s.views(ctx, rows)
}

func validateGroupInput(input GroupInput) (string, string, error) {
	name := strings.TrimSpace(input.Name)
	description := strings.TrimSpace(input.Description)
	if name == "" || len(name) > 128 {
		return "", "", errors.New("node group name is required and must not exceed 128 characters")
	}
	if len(description) > 512 {
		return "", "", errors.New("node group description must not exceed 512 characters")
	}
	return name, description, nil
}

func groupNameExists(db *gorm.DB, name string, excludeID uint) bool {
	query := db.Model(&database.NodeGroup{}).Where("LOWER(name) = LOWER(?)", name)
	if excludeID != 0 {
		query = query.Where("id <> ?", excludeID)
	}
	var count int64
	return query.Count(&count).Error == nil && count > 0
}

func groupView(row database.NodeGroup, count int64) GroupView {
	return GroupView{ID: row.ID, Name: row.Name, Description: row.Description, IsDefault: row.IsDefault, NodeCount: count, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt}
}
