package volume

import (
	"context"
	"errors"
)

var ErrInUse = errors.New("volume is in use")

type Resource struct {
	Name       string            `json:"name"`
	Driver     string            `json:"driver"`
	Mountpoint string            `json:"mountpoint"`
	CreatedAt  string            `json:"created_at"`
	Scope      string            `json:"scope"`
	Labels     map[string]string `json:"labels"`
	UsedBy     []string          `json:"used_by"`
	Size       int64             `json:"size"`
}
type CreateRequest struct {
	Name    string            `json:"name"`
	Driver  string            `json:"driver"`
	Labels  map[string]string `json:"labels"`
	Options map[string]string `json:"options"`
}
type Service interface {
	ListVolumes(context.Context) ([]Resource, error)
	InspectVolume(context.Context, string) (Resource, error)
	CreateVolume(context.Context, CreateRequest) (Resource, error)
	RemoveVolume(context.Context, string) error
}
