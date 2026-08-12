package container

import (
	"context"
	"io"
	"strings"
	"time"
)

type Port struct {
	PrivatePort uint16 `json:"private_port"`
	PublicPort  uint16 `json:"public_port,omitempty"`
	Type        string `json:"type"`
	IP          string `json:"ip,omitempty"`
}
type Mount struct {
	Type        string `json:"type"`
	Name        string `json:"name,omitempty"`
	Source      string `json:"source"`
	Destination string `json:"destination"`
	ReadWrite   bool   `json:"read_write"`
}
type Network struct {
	Name       string `json:"name"`
	IPAddress  string `json:"ip_address"`
	Gateway    string `json:"gateway"`
	MacAddress string `json:"mac_address"`
}

type Summary struct {
	ID            string            `json:"id"`
	Name          string            `json:"name"`
	Image         string            `json:"image"`
	Command       string            `json:"command"`
	Created       time.Time         `json:"created"`
	State         string            `json:"state"`
	Status        string            `json:"status"`
	Ports         []Port            `json:"ports"`
	Labels        map[string]string `json:"labels"`
	CPUPercent    float64           `json:"cpu_percent"`
	MemoryBytes   uint64            `json:"memory_bytes"`
	UptimeSeconds int64             `json:"uptime_seconds"`
}

type Metrics struct {
	ID            string  `json:"id"`
	CPUPercent    float64 `json:"cpu_percent"`
	MemoryBytes   uint64  `json:"memory_bytes"`
	UptimeSeconds int64   `json:"uptime_seconds"`
}

type Detail struct {
	ID               string            `json:"id"`
	Name             string            `json:"name"`
	Image            string            `json:"image"`
	Created          time.Time         `json:"created"`
	State            string            `json:"state"`
	Status           string            `json:"status"`
	PID              int               `json:"pid"`
	Command          []string          `json:"command"`
	Entrypoint       []string          `json:"entrypoint"`
	WorkingDirectory string            `json:"working_directory"`
	RestartPolicy    string            `json:"restart_policy"`
	Environment      []Environment     `json:"environment"`
	Ports            []Port            `json:"ports"`
	Mounts           []Mount           `json:"mounts"`
	Networks         []Network         `json:"networks"`
	Labels           map[string]string `json:"labels"`
}

type Environment struct {
	Key       string `json:"key"`
	Value     string `json:"value,omitempty"`
	Sensitive bool   `json:"sensitive"`
}

type Terminal interface {
	io.ReadWriteCloser
	Resize(context.Context, uint, uint) error
}

func MaskEnvironment(values []string) []Environment {
	result := make([]Environment, 0, len(values))
	for _, item := range values {
		key, value, _ := strings.Cut(item, "=")
		upper := strings.ToUpper(key)
		sensitive := strings.Contains(upper, "PASSWORD") || strings.Contains(upper, "TOKEN") || strings.Contains(upper, "SECRET") || strings.Contains(upper, "API_KEY") || strings.Contains(upper, "PRIVATE_KEY")
		if sensitive {
			value = ""
		}
		result = append(result, Environment{Key: key, Value: value, Sensitive: sensitive})
	}
	return result
}

type Service interface {
	List(context.Context) ([]Summary, error)
	Metrics(context.Context) ([]Metrics, error)
	Get(context.Context, string) (Detail, error)
	Start(context.Context, string) error
	Stop(context.Context, string) error
	Restart(context.Context, string) error
	Pause(context.Context, string) error
	Unpause(context.Context, string) error
	Kill(context.Context, string) error
	Rename(context.Context, string, string) error
	Remove(context.Context, string, bool) error
	Logs(context.Context, string, string, string) (io.ReadCloser, error)
	Stats(context.Context, string) (io.ReadCloser, error)
	Terminal(context.Context, string, uint, uint) (Terminal, error)
}
