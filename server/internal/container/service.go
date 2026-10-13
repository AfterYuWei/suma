package container

import (
	"context"
	"github.com/suma/suma/server/internal/redact"
	"io"
	"net/url"
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
	Anonymous   *bool  `json:"anonymous"`
}
type Network struct {
	Name        string `json:"name"`
	IPAddress   string `json:"ip_address"`
	Gateway     string `json:"gateway"`
	MacAddress  string `json:"mac_address"`
	IPv6Address string `json:"ipv6_address"`
	IPv6Gateway string `json:"ipv6_gateway"`
}

type NetworkSummary struct {
	Name        string `json:"name"`
	IPAddress   string `json:"ip_address,omitempty"`
	IPv6Address string `json:"ipv6_address,omitempty"`
}

type Summary struct {
	NetworkAvailable bool              `json:"-"`
	BlockAvailable   bool              `json:"-"`
	PIDsAvailable    bool              `json:"-"`
	CPUAvailable     bool              `json:"cpu_available"`
	MemoryAvailable  bool              `json:"memory_available"`
	ID               string            `json:"id"`
	Name             string            `json:"name"`
	Image            string            `json:"image"`
	ImageID          string            `json:"image_id,omitempty"`
	Command          string            `json:"command"`
	Created          time.Time         `json:"created"`
	State            string            `json:"state"`
	Status           string            `json:"status"`
	Ports            []Port            `json:"ports"`
	Networks         []NetworkSummary  `json:"networks"`
	Labels           map[string]string `json:"labels"`
	CPUPercent       float64           `json:"cpu_percent"`
	MemoryBytes      uint64            `json:"memory_bytes"`
	UptimeSeconds    int64             `json:"uptime_seconds"`
	NetworkRXBytes   uint64            `json:"network_rx_bytes"`
	NetworkTXBytes   uint64            `json:"network_tx_bytes"`
	BlockReadBytes   uint64            `json:"block_read_bytes"`
	BlockWriteBytes  uint64            `json:"block_write_bytes"`
	PIDs             uint64            `json:"pids"`
	SampledAt        *time.Time        `json:"sampled_at,omitempty"`
	StartedAt        *time.Time        `json:"started_at,omitempty"`
}

type Metrics struct {
	MemoryLimitBytes *int64     `json:"memory_limit_bytes,omitempty"`
	NetworkAvailable bool       `json:"network_available"`
	BlockAvailable   bool       `json:"block_available"`
	PIDsAvailable    bool       `json:"pids_available"`
	CPUAvailable     bool       `json:"cpu_available"`
	MemoryAvailable  bool       `json:"memory_available"`
	ID               string     `json:"id"`
	Name             string     `json:"name"`
	Image            string     `json:"image"`
	State            string     `json:"state"`
	Available        bool       `json:"available"`
	CPUPercent       float64    `json:"cpu_percent"`
	MemoryBytes      uint64     `json:"memory_bytes"`
	UptimeSeconds    int64      `json:"uptime_seconds"`
	NetworkRXBytes   uint64     `json:"network_rx_bytes"`
	NetworkTXBytes   uint64     `json:"network_tx_bytes"`
	BlockReadBytes   uint64     `json:"block_read_bytes"`
	BlockWriteBytes  uint64     `json:"block_write_bytes"`
	PIDs             uint64     `json:"pids"`
	SampledAt        *time.Time `json:"sampled_at,omitempty"`
	StartedAt        *time.Time `json:"started_at,omitempty"`
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
	NetworkMode      string            `json:"network_mode"`
	ReadOnlyRootFS   bool              `json:"read_only_rootfs"`
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
		sensitive := SensitiveEnvironmentKey(key) || redact.Text(value) != value
		if address, err := url.Parse(value); err == nil && address.User != nil {
			sensitive = true
		}
		if sensitive {
			value = ""
		}
		result = append(result, Environment{Key: key, Value: value, Sensitive: sensitive})
	}
	return result
}

func SensitiveEnvironmentKey(key string) bool {
	upper := strings.ToUpper(strings.ReplaceAll(key, "-", "_"))
	compact := strings.ReplaceAll(upper, "_", "")
	for _, marker := range []string{"PASSWORD", "PASSWD", "TOKEN", "SECRET", "APIKEY", "PRIVATEKEY", "ACCESSKEY", "CREDENTIAL", "AUTHORIZATION", "COOKIE", "SESSIONKEY", "ENCRYPTIONKEY", "SIGNINGKEY"} {
		if strings.Contains(compact, marker) {
			return true
		}
	}
	return upper == "PASS" || strings.HasSuffix(upper, "_PASS") || upper == "KEY" || (strings.HasSuffix(upper, "_KEY") && !strings.Contains(upper, "PUBLIC"))
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

// ForceRemover is implemented by runtimes that can remove a running
// container after an explicit destructive confirmation. Attached volumes are
// controlled independently by the volumes argument.
type ForceRemover interface {
	ForceRemove(context.Context, string, bool) error
}
