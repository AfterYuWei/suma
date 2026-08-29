package network

import "context"

type IPAM struct {
	Subnet  string `json:"subnet"`
	Gateway string `json:"gateway"`
}
type AttachedContainer struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	IPv4Address string `json:"ipv4_address"`
	IPv6Address string `json:"ipv6_address"`
}
type Resource struct {
	ID                 string              `json:"id"`
	Name               string              `json:"name"`
	Driver             string              `json:"driver"`
	Scope              string              `json:"scope"`
	IPv6               bool                `json:"ipv6"`
	Internal           bool                `json:"internal"`
	IPAM               []IPAM              `json:"ipam"`
	Containers         int                 `json:"containers"`
	AttachedContainers []AttachedContainer `json:"attached_containers"`
	Labels             map[string]string   `json:"labels"`
}
type CreateRequest struct {
	Name    string `json:"name"`
	Driver  string `json:"driver"`
	Subnet  string `json:"subnet"`
	Gateway string `json:"gateway"`
	IPv6    bool   `json:"ipv6"`
}
type Service interface {
	ListNetworks(context.Context) ([]Resource, error)
	InspectNetwork(context.Context, string) (Resource, error)
	CreateNetwork(context.Context, CreateRequest) (Resource, error)
	RemoveNetwork(context.Context, string) error
}
