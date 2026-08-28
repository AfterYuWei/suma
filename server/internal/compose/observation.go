package compose

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	ProjectLabel         = "com.docker.compose.project"
	ServiceLabel         = "com.docker.compose.service"
	WorkingDirLabel      = "com.docker.compose.project.working_dir"
	ConfigFilesLabel     = "com.docker.compose.project.config_files"
	ContainerNumberLabel = "com.docker.compose.container-number"
	ConfigHashLabel      = "com.docker.compose.config-hash"
	OneOffLabel          = "com.docker.compose.oneoff"
)

type RuntimeInspector interface {
	InspectComposeProject(context.Context, string) (RuntimeProjectSnapshot, error)
}

type RuntimeProjectSnapshot struct {
	ProjectName string             `json:"project_name"`
	Containers  []RuntimeContainer `json:"containers"`
	Networks    []RuntimeNetwork   `json:"networks"`
	Volumes     []RuntimeVolume    `json:"volumes"`
	Warnings    []string           `json:"warnings,omitempty"`
}

type RuntimeContainer struct {
	ID               string            `json:"id"`
	Name             string            `json:"name"`
	Service          string            `json:"service"`
	ContainerNumber  int               `json:"container_number"`
	ConfigHash       string            `json:"config_hash"`
	OneOff           bool              `json:"one_off"`
	CreatedAt        time.Time         `json:"created_at"`
	State            string            `json:"state"`
	ImageReference   string            `json:"image_reference"`
	ImageID          string            `json:"image_id"`
	ImageEnvironment []string          `json:"image_environment,omitempty"`
	ImageInspectOK   bool              `json:"image_inspect_ok"`
	Config           RuntimeConfig     `json:"config"`
	Labels           map[string]string `json:"labels"`
}

type RuntimeConfig struct {
	Image            string            `json:"image"`
	Command          []string          `json:"command,omitempty"`
	Entrypoint       []string          `json:"entrypoint,omitempty"`
	User             string            `json:"user,omitempty"`
	WorkingDirectory string            `json:"working_directory,omitempty"`
	Environment      []string          `json:"environment,omitempty"`
	Ports            []RuntimePort     `json:"ports,omitempty"`
	Mounts           []RuntimeMount    `json:"mounts,omitempty"`
	Networks         []RuntimeEndpoint `json:"networks,omitempty"`
	Restart          RuntimeRestart    `json:"restart,omitempty"`
	Healthcheck      *RuntimeHealth    `json:"healthcheck,omitempty"`
	StopSignal       string            `json:"stop_signal,omitempty"`
	StopTimeout      int               `json:"stop_timeout,omitempty"`
	Hostname         string            `json:"hostname,omitempty"`
	DomainName       string            `json:"domain_name,omitempty"`
	DNS              []string          `json:"dns,omitempty"`
	DNSSearch        []string          `json:"dns_search,omitempty"`
	DNSOptions       []string          `json:"dns_options,omitempty"`
	ExtraHosts       []string          `json:"extra_hosts,omitempty"`
	Sysctls          map[string]string `json:"sysctls,omitempty"`
	CapAdd           []string          `json:"cap_add,omitempty"`
	CapDrop          []string          `json:"cap_drop,omitempty"`
	SecurityOptions  []string          `json:"security_options,omitempty"`
	Groups           []string          `json:"groups,omitempty"`
	Devices          []RuntimeDevice   `json:"devices,omitempty"`
	Ulimits          []RuntimeUlimit   `json:"ulimits,omitempty"`
	Privileged       bool              `json:"privileged,omitempty"`
	ReadOnly         bool              `json:"read_only,omitempty"`
	TTY              bool              `json:"tty,omitempty"`
	StdinOpen        bool              `json:"stdin_open,omitempty"`
	Init             *bool             `json:"init,omitempty"`
	ShmSize          int64             `json:"shm_size,omitempty"`
	NetworkMode      string            `json:"network_mode,omitempty"`
	PIDMode          string            `json:"pid_mode,omitempty"`
	IPCMode          string            `json:"ipc_mode,omitempty"`
	Runtime          string            `json:"runtime,omitempty"`
	Resources        RuntimeResources  `json:"resources,omitempty"`
	Logging          RuntimeLogging    `json:"logging,omitempty"`
	Labels           map[string]string `json:"labels,omitempty"`
}

type RuntimePort struct {
	Target    uint16 `json:"target"`
	Published uint16 `json:"published,omitempty"`
	Protocol  string `json:"protocol"`
	HostIP    string `json:"host_ip,omitempty"`
}

type RuntimeMount struct {
	Type        string `json:"type"`
	Name        string `json:"name,omitempty"`
	Source      string `json:"source"`
	Target      string `json:"target"`
	ReadOnly    bool   `json:"read_only"`
	Propagation string `json:"propagation,omitempty"`
}

type RuntimeEndpoint struct {
	Name    string   `json:"name"`
	Aliases []string `json:"aliases,omitempty"`
}

type RuntimeRestart struct {
	Name              string `json:"name,omitempty"`
	MaximumRetryCount int    `json:"maximum_retry_count,omitempty"`
}

type RuntimeHealth struct {
	Test          []string `json:"test,omitempty"`
	Interval      int64    `json:"interval,omitempty"`
	Timeout       int64    `json:"timeout,omitempty"`
	StartPeriod   int64    `json:"start_period,omitempty"`
	StartInterval int64    `json:"start_interval,omitempty"`
	Retries       int      `json:"retries,omitempty"`
}

type RuntimeDevice struct {
	Source      string `json:"source"`
	Destination string `json:"destination"`
	Permissions string `json:"permissions"`
}

type RuntimeUlimit struct {
	Name string `json:"name"`
	Soft int64  `json:"soft"`
	Hard int64  `json:"hard"`
}

type RuntimeResources struct {
	CPUShares         int64  `json:"cpu_shares,omitempty"`
	NanoCPUs          int64  `json:"nano_cpus,omitempty"`
	CPUPeriod         int64  `json:"cpu_period,omitempty"`
	CPUQuota          int64  `json:"cpu_quota,omitempty"`
	CPUSet            string `json:"cpu_set,omitempty"`
	Memory            int64  `json:"memory,omitempty"`
	MemoryReservation int64  `json:"memory_reservation,omitempty"`
	MemorySwap        int64  `json:"memory_swap,omitempty"`
	PidsLimit         *int64 `json:"pids_limit,omitempty"`
}

type RuntimeLogging struct {
	Driver  string            `json:"driver,omitempty"`
	Options map[string]string `json:"options,omitempty"`
}

type RuntimeNetwork struct {
	ID       string            `json:"id"`
	Name     string            `json:"name"`
	Driver   string            `json:"driver"`
	Internal bool              `json:"internal"`
	Labels   map[string]string `json:"labels"`
}

type RuntimeVolume struct {
	Name    string            `json:"name"`
	Driver  string            `json:"driver"`
	Scope   string            `json:"scope"`
	Labels  map[string]string `json:"labels"`
	Options map[string]string `json:"options,omitempty"`
}

type ObservedComposeProject struct {
	Name             string                   `json:"name"`
	Services         []ObservedComposeService `json:"services"`
	Networks         []RuntimeNetwork         `json:"networks"`
	Volumes          []RuntimeVolume          `json:"volumes"`
	OneOffContainers []ContainerInstance      `json:"one_off_containers"`
	OrphanContainers []ContainerInstance      `json:"orphan_containers"`
	Warnings         []string                 `json:"warnings,omitempty"`
	Fingerprint      string                   `json:"fingerprint"`
}

type ObservedComposeService struct {
	Name             string              `json:"name"`
	Declared         bool                `json:"declared"`
	DesiredReplicas  int                 `json:"desired_replicas"`
	Instances        []ContainerInstance `json:"instances"`
	ConfigVariants   []ConfigVariant     `json:"config_variants"`
	CanonicalVariant string              `json:"canonical_variant"`
	DriftStatus      string              `json:"drift_status"`
	DriftReasons     []string            `json:"drift_reasons,omitempty"`
	DriftFields      []string            `json:"drift_fields,omitempty"`
	ExpectedConfig   map[string]any      `json:"expected_config,omitempty"`
}

type ContainerInstance struct {
	ContainerID     string        `json:"container_id"`
	ContainerName   string        `json:"container_name"`
	ContainerNumber int           `json:"container_number"`
	ConfigHash      string        `json:"config_hash"`
	State           string        `json:"state"`
	CreatedAt       time.Time     `json:"created_at"`
	OneOff          bool          `json:"one_off"`
	Variant         string        `json:"variant"`
	RuntimeConfig   RuntimeConfig `json:"runtime_config"`
}

type ConfigVariant struct {
	Fingerprint      string        `json:"fingerprint"`
	Instances        []string      `json:"instances"`
	DifferenceFields []string      `json:"difference_fields,omitempty"`
	Config           RuntimeConfig `json:"config"`
}

type runtimeVariantGroup struct {
	config    RuntimeConfig
	instances []ContainerInstance
	newest    time.Time
}

func ObserveRuntimeProject(snapshot RuntimeProjectSnapshot) ObservedComposeProject {
	observed := ObservedComposeProject{Name: snapshot.ProjectName, Networks: snapshot.Networks, Volumes: snapshot.Volumes, Warnings: snapshot.Warnings}
	byService := map[string][]RuntimeContainer{}
	for _, container := range snapshot.Containers {
		if container.OneOff {
			observed.OneOffContainers = append(observed.OneOffContainers, instanceFromRuntime(container, runtimeConfigFingerprint(container.Config)))
			continue
		}
		if strings.TrimSpace(container.Service) == "" {
			observed.OrphanContainers = append(observed.OrphanContainers, instanceFromRuntime(container, runtimeConfigFingerprint(container.Config)))
			continue
		}
		byService[container.Service] = append(byService[container.Service], container)
	}
	serviceNames := make([]string, 0, len(byService))
	for name := range byService {
		serviceNames = append(serviceNames, name)
	}
	sort.Strings(serviceNames)
	for _, name := range serviceNames {
		observed.Services = append(observed.Services, observeRuntimeService(name, byService[name]))
	}
	sortInstances(observed.OneOffContainers)
	sortInstances(observed.OrphanContainers)
	observed.Fingerprint = observationFingerprint(snapshot)
	return observed
}

func observeRuntimeService(name string, containers []RuntimeContainer) ObservedComposeService {
	groups := map[string]*runtimeVariantGroup{}
	for _, container := range containers {
		fingerprint := runtimeConfigFingerprint(container.Config)
		instance := instanceFromRuntime(container, fingerprint)
		entry := groups[fingerprint]
		if entry == nil {
			entry = &runtimeVariantGroup{config: container.Config}
			groups[fingerprint] = entry
		}
		entry.instances = append(entry.instances, instance)
		if container.CreatedAt.After(entry.newest) {
			entry.newest = container.CreatedAt
		}
	}
	keys := make([]string, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		left, right := groups[keys[i]], groups[keys[j]]
		if len(left.instances) != len(right.instances) {
			return len(left.instances) > len(right.instances)
		}
		if !left.newest.Equal(right.newest) {
			return left.newest.After(right.newest)
		}
		return keys[i] < keys[j]
	})
	service := ObservedComposeService{Name: name, DriftStatus: "in_sync"}
	if len(keys) > 0 {
		service.CanonicalVariant = keys[0]
		service.DesiredReplicas = len(groups[keys[0]].instances)
	}
	if len(keys) > 1 {
		service.DriftStatus = "runtime_drift"
		service.DriftReasons = []string{runtimeDriftReason(groups)}
	}
	var canonical RuntimeConfig
	if len(keys) > 0 {
		canonical = groups[keys[0]].config
	}
	for _, key := range keys {
		entry := groups[key]
		sortInstances(entry.instances)
		ids := make([]string, len(entry.instances))
		for index, instance := range entry.instances {
			ids[index] = instance.ContainerID
		}
		service.Instances = append(service.Instances, entry.instances...)
		differenceFields := runtimeConfigDifferenceFields(canonical, entry.config)
		service.DriftFields = appendUniqueStrings(service.DriftFields, differenceFields...)
		service.ConfigVariants = append(service.ConfigVariants, ConfigVariant{Fingerprint: key, Instances: ids, DifferenceFields: differenceFields, Config: entry.config})
	}
	sortInstances(service.Instances)
	return service
}

func runtimeDriftReason(groups map[string]*runtimeVariantGroup) string {
	configHashes := map[string]bool{}
	for _, entry := range groups {
		for _, instance := range entry.instances {
			if instance.ConfigHash != "" {
				configHashes[instance.ConfigHash] = true
			}
		}
	}
	if len(configHashes) > 1 {
		return "partial_recreate"
	}
	if len(configHashes) == 1 {
		return "manual_modification"
	}
	return "runtime_drift"
}

func runtimeConfigDifferenceFields(left, right RuntimeConfig) []string {
	left.Hostname, right.Hostname = "", ""
	leftContent, _ := json.Marshal(left)
	rightContent, _ := json.Marshal(right)
	leftMap, rightMap := map[string]json.RawMessage{}, map[string]json.RawMessage{}
	_ = json.Unmarshal(leftContent, &leftMap)
	_ = json.Unmarshal(rightContent, &rightMap)
	keys := map[string]bool{}
	for key := range leftMap {
		keys[key] = true
	}
	for key := range rightMap {
		keys[key] = true
	}
	result := make([]string, 0, len(keys))
	for key := range keys {
		if string(leftMap[key]) != string(rightMap[key]) {
			result = append(result, key)
		}
	}
	sort.Strings(result)
	return result
}

func appendUniqueStrings(values []string, additions ...string) []string {
	known := make(map[string]bool, len(values)+len(additions))
	for _, value := range values {
		known[value] = true
	}
	for _, value := range additions {
		if value != "" && !known[value] {
			values = append(values, value)
			known[value] = true
		}
	}
	sort.Strings(values)
	return values
}

func instanceFromRuntime(container RuntimeContainer, variant string) ContainerInstance {
	return ContainerInstance{
		ContainerID: container.ID, ContainerName: container.Name, ContainerNumber: container.ContainerNumber,
		ConfigHash: container.ConfigHash, State: container.State, CreatedAt: container.CreatedAt,
		OneOff: container.OneOff, Variant: variant, RuntimeConfig: container.Config,
	}
}

func sortInstances(values []ContainerInstance) {
	sort.Slice(values, func(i, j int) bool {
		if values[i].ContainerNumber != values[j].ContainerNumber {
			return values[i].ContainerNumber < values[j].ContainerNumber
		}
		return values[i].ContainerID < values[j].ContainerID
	})
}

func runtimeConfigFingerprint(config RuntimeConfig) string {
	// Hostname is normally generated from the replica name, so it must not split
	// otherwise-identical scaled containers into separate service definitions.
	config.Hostname = ""
	content, _ := json.Marshal(config)
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

func observationFingerprint(snapshot RuntimeProjectSnapshot) string {
	copy := snapshot
	sort.Slice(copy.Containers, func(i, j int) bool { return copy.Containers[i].ID < copy.Containers[j].ID })
	sort.Slice(copy.Networks, func(i, j int) bool { return copy.Networks[i].ID < copy.Networks[j].ID })
	sort.Slice(copy.Volumes, func(i, j int) bool { return copy.Volumes[i].Name < copy.Volumes[j].Name })
	content, _ := json.Marshal(copy)
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

func labelNumber(labels map[string]string) int {
	value, _ := strconv.Atoi(strings.TrimSpace(labels[ContainerNumberLabel]))
	return value
}

func labelBool(labels map[string]string, key string) bool {
	value := strings.ToLower(strings.TrimSpace(labels[key]))
	return value == "true" || value == "1" || value == "yes"
}
