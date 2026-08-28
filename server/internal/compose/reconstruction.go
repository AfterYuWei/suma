package compose

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/goccy/go-yaml"
	projectdomain "github.com/suma/suma/server/internal/project"
)

type EnvironmentDestination string

const (
	EnvironmentCompose EnvironmentDestination = "compose"
	EnvironmentFile    EnvironmentDestination = "env"
	EnvironmentExclude EnvironmentDestination = "exclude"
)

type EnvironmentCandidate struct {
	ID          string                 `json:"id"`
	Service     string                 `json:"service"`
	Key         string                 `json:"key"`
	Value       string                 `json:"value"`
	Source      string                 `json:"source"`
	Sensitive   bool                   `json:"sensitive"`
	Destination EnvironmentDestination `json:"destination"`
	Reason      string                 `json:"reason"`
}

type EnvironmentChoice struct {
	ID          string                 `json:"id"`
	Destination EnvironmentDestination `json:"destination"`
}

type ProjectTakeoverDraft struct {
	ProjectName  string                     `json:"project_name"`
	Backend      string                     `json:"backend"`
	Source       string                     `json:"source"`
	Confidence   string                     `json:"confidence"`
	Fingerprint  string                     `json:"fingerprint"`
	Compose      string                     `json:"compose"`
	Environment  string                     `json:"environment"`
	Variables    []EnvironmentCandidate     `json:"variables"`
	Warnings     []string                   `json:"warnings"`
	Blockers     []string                   `json:"blockers"`
	Capabilities []projectdomain.Capability `json:"capabilities"`
	Observation  ObservedComposeProject     `json:"observation"`
	model        map[string]any
}

func (s *Service) BuildTakeoverDraft(ctx context.Context, name string) (ProjectTakeoverDraft, error) {
	project, err := s.findProject(ctx, name)
	if err != nil {
		return ProjectTakeoverDraft{}, err
	}
	if project.CanManage {
		return ProjectTakeoverDraft{}, fmt.Errorf("Project is already managed by SUMA")
	}
	inspector, ok := s.containers.(RuntimeInspector)
	if !ok {
		return ProjectTakeoverDraft{}, fmt.Errorf("Docker runtime does not support Compose Project inspection")
	}
	snapshot, err := inspector.InspectComposeProject(ctx, name)
	if err != nil {
		return ProjectTakeoverDraft{}, err
	}
	if len(snapshot.Containers) == 0 {
		return ProjectTakeoverDraft{}, fmt.Errorf("Compose Project has no observable containers")
	}
	observation := ObserveRuntimeProject(snapshot)
	draft := ProjectTakeoverDraft{
		ProjectName: name, Backend: "compose", Source: "runtime", Confidence: "medium", Observation: observation,
		Variables: []EnvironmentCandidate{}, Warnings: append([]string{}, observation.Warnings...), Blockers: []string{}, Capabilities: []projectdomain.Capability{},
	}
	var model map[string]any
	if s.localSources && s.runner != nil && project.Path != "" && len(project.ConfigFiles) > 0 {
		manifest, sourceErr := ValidateLocalProjectSource(project.Path, project.ConfigFiles)
		if sourceErr == nil {
			spec := ExecutionSpec{ProjectName: name, ProjectDir: manifest.WorkingDirectory, Files: manifest.ConfigFiles, Profiles: []string{"*"}}
			rendered, renderErr := s.runner.Render(ctx, spec, io.Discard)
			if renderErr == nil {
				model, renderErr = decodeComposeModel(rendered)
			}
			if renderErr == nil {
				draft.Source, draft.Confidence = "mapped", "high"
				draft.Variables = extractMappedEnvironment(model)
				removeUnsupportedSourceFeatures(model, &draft)
				var hashes map[string]string
				if hashRunner, ok := s.runner.(interface {
					Hashes(context.Context, ExecutionSpec, io.Writer) (map[string]string, error)
				}); ok {
					if renderedHashes, hashErr := hashRunner.Hashes(ctx, spec, io.Discard); hashErr == nil {
						hashes = renderedHashes
					} else {
						draft.Warnings = append(draft.Warnings, "Unable to compare rendered service hashes with running containers")
					}
				} else {
					draft.Warnings = append(draft.Warnings, "Unable to compare rendered service hashes with running containers")
				}
				applyExpectedProject(&draft.Observation, hashes, model)
			} else {
				draft.Warnings = append(draft.Warnings, "Mapped Compose source could not be rendered; the whole Project was rebuilt from runtime metadata")
			}
		} else {
			draft.Warnings = append(draft.Warnings, "Mapped Compose source was not safe or complete; the whole Project was rebuilt from runtime metadata: "+safeError(sourceErr))
		}
	}
	draft.Warnings = appendUniqueStrings(draft.Warnings, draft.Observation.Warnings...)
	if model == nil {
		model, draft.Variables = runtimeComposeModel(name, snapshot, observation)
		if hasRuntimeDrift(observation) {
			draft.Confidence = "low"
			draft.Warnings = append(draft.Warnings, "One or more services have runtime drift; the majority configuration was selected for the draft")
		}
		draft.Warnings = append(draft.Warnings, "Runtime reconstruction cannot recover comments, YAML anchors, source variable expressions, profiles, dependencies, build contexts, or removed services")
	}
	model["name"] = name
	draft.model = model
	draft.Fingerprint = takeoverFingerprint(observation.Fingerprint, draft.Source, model)
	draft.Compose, draft.Environment, err = renderTakeoverModel(model, draft.Variables)
	if err != nil {
		return ProjectTakeoverDraft{}, err
	}
	setTakeoverDraftCapabilities(&draft)
	return draft, nil
}

func (s *Service) RenderTakeoverDraft(ctx context.Context, name, fingerprint string, choices []EnvironmentChoice) (ProjectTakeoverDraft, error) {
	draft, err := s.BuildTakeoverDraft(ctx, name)
	if err != nil {
		return ProjectTakeoverDraft{}, err
	}
	if draft.Fingerprint != fingerprint {
		return ProjectTakeoverDraft{}, fmt.Errorf("Project changed while preparing takeover")
	}
	byID := map[string]EnvironmentDestination{}
	for _, choice := range choices {
		if choice.Destination != EnvironmentCompose && choice.Destination != EnvironmentFile && choice.Destination != EnvironmentExclude {
			return ProjectTakeoverDraft{}, fmt.Errorf("invalid environment destination")
		}
		byID[choice.ID] = choice.Destination
	}
	for index := range draft.Variables {
		if draft.Variables[index].Source == "image_default" {
			draft.Variables[index].Destination = EnvironmentExclude
			continue
		}
		if destination, ok := byID[draft.Variables[index].ID]; ok {
			draft.Variables[index].Destination = destination
		}
	}
	draft.Compose, draft.Environment, err = renderTakeoverModel(draft.model, draft.Variables)
	if err == nil {
		setTakeoverDraftCapabilities(&draft)
	}
	return draft, err
}

func setTakeoverDraftCapabilities(draft *ProjectTakeoverDraft) {
	draft.Capabilities = []projectdomain.Capability{projectdomain.CapabilityTakeover}
	if len(draft.Blockers) != 0 {
		return
	}
	assessment, err := AssessShadowPreview(draft.Compose)
	if err == nil && assessment.Eligible {
		draft.Capabilities = append(draft.Capabilities, projectdomain.CapabilityShadowPreview)
	}
}

func decodeComposeModel(content string) (map[string]any, error) {
	decoder := json.NewDecoder(strings.NewReader(content))
	decoder.UseNumber()
	var value map[string]any
	if err := decoder.Decode(&value); err != nil {
		return nil, fmt.Errorf("decode normalized Compose Project: %w", err)
	}
	if _, ok := value["services"].(map[string]any); !ok {
		return nil, fmt.Errorf("normalized Compose Project has no services")
	}
	delete(value, "version")
	return value, nil
}

func extractMappedEnvironment(model map[string]any) []EnvironmentCandidate {
	services, _ := model["services"].(map[string]any)
	result := []EnvironmentCandidate{}
	for _, serviceName := range sortedMapKeys(services) {
		service, _ := services[serviceName].(map[string]any)
		environment, _ := service["environment"].(map[string]any)
		for _, key := range sortedMapKeys(environment) {
			value := environmentValue(environment[key])
			result = append(result, newEnvironmentCandidate(serviceName, key, value, "compose_explicit"))
		}
		delete(service, "environment")
		delete(service, "env_file")
	}
	return result
}

func runtimeComposeModel(name string, snapshot RuntimeProjectSnapshot, observation ObservedComposeProject) (map[string]any, []EnvironmentCandidate) {
	model := map[string]any{"name": name, "services": map[string]any{}}
	services := model["services"].(map[string]any)
	byContainer := map[string]RuntimeContainer{}
	for _, container := range snapshot.Containers {
		byContainer[container.ID] = container
	}
	variables := []EnvironmentCandidate{}
	for _, observed := range observation.Services {
		if len(observed.ConfigVariants) == 0 {
			continue
		}
		config := observed.ConfigVariants[0].Config
		service := runtimeServiceModel(config, observed.DesiredReplicas)
		services[observed.Name] = service
		if len(observed.ConfigVariants[0].Instances) == 0 {
			continue
		}
		container := byContainer[observed.ConfigVariants[0].Instances[0]]
		variables = append(variables, inferRuntimeEnvironment(observed.Name, container.Config.Environment, container.ImageEnvironment, container.ImageInspectOK)...)
		delete(service, "environment")
	}
	addRuntimeResources(model, name, observation)
	return model, variables
}

func runtimeServiceModel(config RuntimeConfig, replicas int) map[string]any {
	service := map[string]any{"image": config.Image}
	explicitNetworkMode := isExplicitNetworkMode(config.NetworkMode)
	setIf(service, "command", config.Command, len(config.Command) > 0)
	setIf(service, "entrypoint", config.Entrypoint, len(config.Entrypoint) > 0)
	setIf(service, "user", config.User, config.User != "")
	setIf(service, "working_dir", config.WorkingDirectory, config.WorkingDirectory != "")
	setIf(service, "hostname", config.Hostname, config.Hostname != "")
	setIf(service, "domainname", config.DomainName, config.DomainName != "")
	setIf(service, "dns", config.DNS, len(config.DNS) > 0)
	setIf(service, "dns_search", config.DNSSearch, len(config.DNSSearch) > 0)
	setIf(service, "dns_opt", config.DNSOptions, len(config.DNSOptions) > 0)
	setIf(service, "extra_hosts", config.ExtraHosts, len(config.ExtraHosts) > 0)
	setIf(service, "sysctls", config.Sysctls, len(config.Sysctls) > 0)
	setIf(service, "cap_add", config.CapAdd, len(config.CapAdd) > 0)
	setIf(service, "cap_drop", config.CapDrop, len(config.CapDrop) > 0)
	setIf(service, "security_opt", config.SecurityOptions, len(config.SecurityOptions) > 0)
	setIf(service, "group_add", config.Groups, len(config.Groups) > 0)
	setIf(service, "privileged", true, config.Privileged)
	setIf(service, "read_only", true, config.ReadOnly)
	setIf(service, "tty", true, config.TTY)
	setIf(service, "stdin_open", true, config.StdinOpen)
	setIf(service, "init", config.Init, config.Init != nil)
	setIf(service, "shm_size", config.ShmSize, config.ShmSize > 0)
	setIf(service, "network_mode", config.NetworkMode, explicitNetworkMode)
	setIf(service, "pid", config.PIDMode, config.PIDMode != "")
	setIf(service, "ipc", config.IPCMode, config.IPCMode != "")
	setIf(service, "runtime", config.Runtime, config.Runtime != "" && config.Runtime != "runc")
	setIf(service, "labels", config.Labels, len(config.Labels) > 0)
	if replicas > 1 {
		service["scale"] = replicas
	}
	if config.Restart.Name != "" && config.Restart.Name != "no" {
		service["restart"] = config.Restart.Name
	}
	if config.Healthcheck != nil {
		health := map[string]any{"test": config.Healthcheck.Test}
		setDuration(health, "interval", config.Healthcheck.Interval)
		setDuration(health, "timeout", config.Healthcheck.Timeout)
		setDuration(health, "start_period", config.Healthcheck.StartPeriod)
		setDuration(health, "start_interval", config.Healthcheck.StartInterval)
		setIf(health, "retries", config.Healthcheck.Retries, config.Healthcheck.Retries > 0)
		service["healthcheck"] = health
	}
	setIf(service, "stop_signal", config.StopSignal, config.StopSignal != "")
	if config.StopTimeout > 0 {
		service["stop_grace_period"] = strconv.Itoa(config.StopTimeout) + "s"
	}
	ports, exposed := []any{}, []string{}
	for _, port := range config.Ports {
		if port.Published == 0 {
			exposed = append(exposed, strconv.Itoa(int(port.Target))+protocolSuffix(port.Protocol))
			continue
		}
		entry := map[string]any{"target": port.Target, "published": port.Published, "protocol": port.Protocol}
		setIf(entry, "host_ip", port.HostIP, port.HostIP != "")
		ports = append(ports, entry)
	}
	setIf(service, "ports", ports, len(ports) > 0)
	setIf(service, "expose", exposed, len(exposed) > 0)
	volumes := []any{}
	for _, mount := range config.Mounts {
		entry := map[string]any{"type": mount.Type, "source": mount.Source, "target": mount.Target}
		setIf(entry, "read_only", true, mount.ReadOnly)
		if mount.Propagation != "" {
			entry["bind"] = map[string]any{"propagation": mount.Propagation}
		}
		volumes = append(volumes, entry)
	}
	setIf(service, "volumes", volumes, len(volumes) > 0)
	networks := map[string]any{}
	if !explicitNetworkMode {
		for _, network := range config.Networks {
			entry := map[string]any{}
			setIf(entry, "aliases", network.Aliases, len(network.Aliases) > 0)
			networks[network.Name] = entry
		}
	}
	setIf(service, "networks", networks, len(networks) > 0)
	devices := []string{}
	for _, device := range config.Devices {
		devices = append(devices, device.Source+":"+device.Destination+":"+device.Permissions)
	}
	setIf(service, "devices", devices, len(devices) > 0)
	ulimits := map[string]any{}
	for _, limit := range config.Ulimits {
		ulimits[limit.Name] = map[string]any{"soft": limit.Soft, "hard": limit.Hard}
	}
	setIf(service, "ulimits", ulimits, len(ulimits) > 0)
	resources := config.Resources
	setIf(service, "cpu_shares", resources.CPUShares, resources.CPUShares > 0)
	setIf(service, "cpus", float64(resources.NanoCPUs)/1e9, resources.NanoCPUs > 0)
	setIf(service, "cpu_period", resources.CPUPeriod, resources.CPUPeriod > 0)
	setIf(service, "cpu_quota", resources.CPUQuota, resources.CPUQuota != 0)
	setIf(service, "cpuset", resources.CPUSet, resources.CPUSet != "")
	setIf(service, "mem_limit", resources.Memory, resources.Memory > 0)
	setIf(service, "mem_reservation", resources.MemoryReservation, resources.MemoryReservation > 0)
	setIf(service, "memswap_limit", resources.MemorySwap, resources.MemorySwap != 0)
	setIf(service, "pids_limit", resources.PidsLimit, resources.PidsLimit != nil)
	if config.Logging.Driver != "" && config.Logging.Driver != "json-file" {
		service["logging"] = map[string]any{"driver": config.Logging.Driver, "options": config.Logging.Options}
	}
	return service
}

func isExplicitNetworkMode(value string) bool {
	return value == "host" || value == "none" || value == "bridge" || strings.HasPrefix(value, "container:") || strings.HasPrefix(value, "service:")
}

func addRuntimeResources(model map[string]any, projectName string, observation ObservedComposeProject) {
	networks, volumes := map[string]any{}, map[string]any{}
	networkAliases, volumeAliases := map[string]string{}, map[string]string{}
	for _, network := range observation.Networks {
		alias := network.Labels["com.docker.compose.network"]
		if alias == "" {
			alias = safeModelName(network.Name)
		}
		networkAliases[network.Name] = alias
		entry := map[string]any{"name": network.Name}
		if network.Labels[ProjectLabel] != projectName {
			entry["external"] = true
		}
		setIf(entry, "driver", network.Driver, network.Driver != "" && network.Driver != "bridge")
		setIf(entry, "internal", true, network.Internal)
		networks[alias] = entry
	}
	for _, volume := range observation.Volumes {
		alias := volume.Labels["com.docker.compose.volume"]
		if alias == "" {
			alias = safeModelName(volume.Name)
		}
		volumeAliases[volume.Name] = alias
		entry := map[string]any{"name": volume.Name}
		if volume.Labels[ProjectLabel] != projectName {
			entry["external"] = true
		}
		setIf(entry, "driver", volume.Driver, volume.Driver != "" && volume.Driver != "local")
		setIf(entry, "driver_opts", volume.Options, len(volume.Options) > 0)
		volumes[alias] = entry
	}
	services, _ := model["services"].(map[string]any)
	for _, raw := range services {
		service, _ := raw.(map[string]any)
		if values, ok := service["networks"].(map[string]any); ok {
			rewritten := map[string]any{}
			for name, value := range values {
				alias := networkAliases[name]
				if alias == "" {
					alias = safeModelName(name)
				}
				rewritten[alias] = value
			}
			service["networks"] = rewritten
		}
		if values, ok := service["volumes"].([]any); ok {
			for _, rawMount := range values {
				mount, _ := rawMount.(map[string]any)
				if mount["type"] == "volume" {
					name, _ := mount["source"].(string)
					if alias := volumeAliases[name]; alias != "" {
						mount["source"] = alias
					}
				}
			}
		}
	}
	setIf(model, "networks", networks, len(networks) > 0)
	setIf(model, "volumes", volumes, len(volumes) > 0)
}

func inferRuntimeEnvironment(service string, runtime, image []string, imageOK bool) []EnvironmentCandidate {
	runtimeValues := environmentMap(runtime)
	imageValues := environmentMap(image)
	result := make([]EnvironmentCandidate, 0, len(runtimeValues))
	for _, key := range sortedMapKeys(runtimeValues) {
		value := runtimeValues[key]
		source := "explicit_inferred"
		if !imageOK {
			source = "unknown"
		} else if imageValue, ok := imageValues[key]; ok && imageValue == value {
			source = "image_default"
		}
		result = append(result, newEnvironmentCandidate(service, key, value, source))
	}
	return result
}

func newEnvironmentCandidate(service, key, value, source string) EnvironmentCandidate {
	sensitive := sensitiveEnvironmentKey(key)
	destination, reason := EnvironmentCompose, "Inferred as an explicit service environment value"
	switch source {
	case "compose_explicit":
		reason = "Declared by the safely rendered Compose Project"
	case "image_default":
		destination, reason = EnvironmentExclude, "Matches the image default environment exactly"
	case "unknown":
		destination, reason = EnvironmentExclude, "Image defaults were unavailable, so the original source cannot be proven"
	default:
		if sensitive {
			destination = EnvironmentFile
		}
	}
	if sensitive && (source == "compose_explicit" || source == "explicit_inferred") {
		destination = EnvironmentFile
	}
	sum := sha256.Sum256([]byte(service + "\x00" + key))
	return EnvironmentCandidate{ID: hex.EncodeToString(sum[:8]), Service: service, Key: key, Value: value, Source: source, Sensitive: sensitive, Destination: destination, Reason: reason}
}

func renderTakeoverModel(base map[string]any, variables []EnvironmentCandidate) (string, string, error) {
	model, err := cloneComposeModel(base)
	if err != nil {
		return "", "", err
	}
	services, _ := model["services"].(map[string]any)
	fileVariables := []EnvironmentCandidate{}
	for _, variable := range variables {
		service, _ := services[variable.Service].(map[string]any)
		if service == nil || variable.Destination == EnvironmentExclude {
			continue
		}
		environment, _ := service["environment"].(map[string]any)
		if environment == nil {
			environment = map[string]any{}
			service["environment"] = environment
		}
		if variable.Destination == EnvironmentCompose {
			environment[variable.Key] = variable.Value
		} else {
			fileVariables = append(fileVariables, variable)
		}
	}
	aliases := environmentAliases(fileVariables)
	for _, variable := range fileVariables {
		service, _ := services[variable.Service].(map[string]any)
		environment, _ := service["environment"].(map[string]any)
		environment[variable.Key] = "${" + aliases[variable.ID] + ":?required}"
	}
	content, err := yaml.Marshal(model)
	if err != nil {
		return "", "", fmt.Errorf("encode Compose takeover draft: %w", err)
	}
	sort.Slice(fileVariables, func(i, j int) bool { return aliases[fileVariables[i].ID] < aliases[fileVariables[j].ID] })
	var environment strings.Builder
	for _, variable := range fileVariables {
		environment.WriteString(aliases[variable.ID])
		environment.WriteByte('=')
		environment.WriteString(quoteDotEnv(variable.Value))
		environment.WriteByte('\n')
	}
	return string(content), environment.String(), nil
}

func environmentAliases(values []EnvironmentCandidate) map[string]string {
	keyValues := map[string]map[string]bool{}
	for _, value := range values {
		if keyValues[value.Key] == nil {
			keyValues[value.Key] = map[string]bool{}
		}
		keyValues[value.Key][value.Value] = true
	}
	result, used, shared := map[string]string{}, map[string]int{}, map[string]string{}
	for _, value := range values {
		signature := value.Key + "\x00" + value.Value
		if alias := shared[signature]; alias != "" {
			result[value.ID] = alias
			continue
		}
		alias := safeEnvironmentName(value.Key)
		if len(keyValues[value.Key]) > 1 {
			alias = safeEnvironmentName(value.Service) + "_" + alias
		}
		used[alias]++
		if used[alias] > 1 {
			alias += "_" + strconv.Itoa(used[alias])
		}
		result[value.ID] = alias
		shared[signature] = alias
	}
	return result
}

func removeUnsupportedSourceFeatures(model map[string]any, draft *ProjectTakeoverDraft) {
	services, _ := model["services"].(map[string]any)
	for _, name := range sortedMapKeys(services) {
		service, _ := services[name].(map[string]any)
		if _, ok := service["build"]; ok {
			delete(service, "build")
			draft.Warnings = append(draft.Warnings, "Service "+name+" build configuration was removed; takeover uses its resolved image")
		}
	}
	for _, section := range []string{"configs", "secrets"} {
		values, _ := model[section].(map[string]any)
		for name, raw := range values {
			entry, _ := raw.(map[string]any)
			if _, ok := entry["file"]; ok {
				draft.Blockers = append(draft.Blockers, fmt.Sprintf("%s %q is file-backed and must be converted to an external resource or supplied inside the managed Project", section, name))
			}
		}
	}
}

func applyExpectedProject(observation *ObservedComposeProject, hashes map[string]string, model map[string]any) {
	services, _ := model["services"].(map[string]any)
	observedNames := map[string]bool{}
	for serviceIndex := range observation.Services {
		service := &observation.Services[serviceIndex]
		observedNames[service.Name] = true
		rawExpected, known := services[service.Name]
		expectedConfig, _ := rawExpected.(map[string]any)
		if !known {
			observation.OrphanContainers = append(observation.OrphanContainers, service.Instances...)
			service.DriftStatus = "orphan"
			service.DriftReasons = appendUniqueStrings(service.DriftReasons, "stale_container")
			continue
		}
		service.Declared = true
		service.ExpectedConfig = expectedConfig
		service.DesiredReplicas = expectedServiceReplicas(expectedConfig)
		if len(service.Instances) > service.DesiredReplicas {
			observation.Warnings = append(observation.Warnings, "Service "+service.Name+" has more running instances than the normalized Compose configuration; a CLI scale override may be active")
		}
		expected := hashes[service.Name]
		if expected == "" {
			continue
		}
		matches, mismatches := []ContainerInstance{}, []ContainerInstance{}
		for _, instance := range service.Instances {
			if instance.ConfigHash != "" && instance.ConfigHash != expected {
				mismatches = append(mismatches, instance)
			} else if instance.ConfigHash == expected {
				matches = append(matches, instance)
			}
		}
		if len(mismatches) == 0 {
			continue
		}
		service.DriftStatus = "runtime_drift"
		reason := "runtime_drift"
		if len(matches) > 0 {
			newestMatch := matches[0].CreatedAt
			allMismatchesOlder := true
			for _, instance := range matches[1:] {
				if instance.CreatedAt.After(newestMatch) {
					newestMatch = instance.CreatedAt
				}
			}
			for _, instance := range mismatches {
				if !instance.CreatedAt.Before(newestMatch) {
					allMismatchesOlder = false
					break
				}
			}
			if allMismatchesOlder {
				reason = "stale_container"
			} else {
				reason = "partial_recreate"
			}
		}
		service.DriftReasons = appendUniqueStrings(service.DriftReasons, reason)
	}
	for _, name := range sortedMapKeys(services) {
		if observedNames[name] {
			continue
		}
		expectedConfig, _ := services[name].(map[string]any)
		observation.Services = append(observation.Services, ObservedComposeService{
			Name:            name,
			Declared:        true,
			DesiredReplicas: expectedServiceReplicas(expectedConfig),
			Instances:       []ContainerInstance{},
			ConfigVariants:  []ConfigVariant{},
			DriftStatus:     "not_created",
			ExpectedConfig:  expectedConfig,
		})
	}
	sort.Slice(observation.Services, func(i, j int) bool { return observation.Services[i].Name < observation.Services[j].Name })
	sortInstances(observation.OrphanContainers)
}

func expectedServiceReplicas(service map[string]any) int {
	if replicas, ok := composeInteger(service["scale"]); ok {
		return replicas
	}
	if deploy, ok := service["deploy"].(map[string]any); ok {
		if replicas, ok := composeInteger(deploy["replicas"]); ok {
			return replicas
		}
	}
	return 1
}

func composeInteger(value any) (int, bool) {
	switch typed := value.(type) {
	case json.Number:
		parsed, err := strconv.Atoi(typed.String())
		return parsed, err == nil && parsed >= 0
	case float64:
		return int(typed), typed >= 0 && typed == float64(int(typed))
	case int:
		return typed, typed >= 0
	case string:
		parsed, err := strconv.Atoi(typed)
		return parsed, err == nil && parsed >= 0
	default:
		return 0, false
	}
}

func hasRuntimeDrift(value ObservedComposeProject) bool {
	for _, service := range value.Services {
		if service.DriftStatus != "in_sync" {
			return true
		}
	}
	return false
}

func takeoverFingerprint(runtime, source string, model map[string]any) string {
	content, _ := json.Marshal(model)
	sum := sha256.Sum256(append([]byte(runtime+"\x00"+source+"\x00"), content...))
	return hex.EncodeToString(sum[:])
}

func cloneComposeModel(value map[string]any) (map[string]any, error) {
	content, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.UseNumber()
	var result map[string]any
	return result, decoder.Decode(&result)
}

func environmentMap(values []string) map[string]string {
	result := map[string]string{}
	for _, item := range values {
		key, value, _ := strings.Cut(item, "=")
		result[key] = value
	}
	return result
}

func environmentValue(value any) string {
	if value == nil {
		return ""
	}
	switch typed := value.(type) {
	case string:
		return typed
	case json.Number:
		return typed.String()
	default:
		return fmt.Sprint(typed)
	}
}

func sensitiveEnvironmentKey(key string) bool {
	upper := strings.ToUpper(key)
	for _, marker := range []string{"PASSWORD", "PASSWD", "TOKEN", "SECRET", "API_KEY", "APIKEY", "PRIVATE_KEY", "ACCESS_KEY", "AUTH"} {
		if strings.Contains(upper, marker) {
			return true
		}
	}
	return false
}

func safeEnvironmentName(value string) string {
	var result strings.Builder
	for _, char := range strings.ToUpper(value) {
		if unicode.IsLetter(char) || unicode.IsDigit(char) || char == '_' {
			result.WriteRune(char)
		} else {
			result.WriteByte('_')
		}
	}
	name := strings.Trim(result.String(), "_")
	if name == "" {
		return "VALUE"
	}
	if name[0] >= '0' && name[0] <= '9' {
		return "VALUE_" + name
	}
	return name
}

func safeModelName(value string) string {
	name := strings.ToLower(safeEnvironmentName(value))
	if name == "" {
		return "resource"
	}
	return name
}

func quoteDotEnv(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "\\'") + "'"
}

func protocolSuffix(value string) string {
	if value == "" || value == "tcp" {
		return ""
	}
	return "/" + value
}

func setIf(target map[string]any, key string, value any, condition bool) {
	if condition {
		target[key] = value
	}
}

func setDuration(target map[string]any, key string, value int64) {
	if value > 0 {
		target[key] = time.Duration(value).String()
	}
}

func sortedMapKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func safeError(err error) string {
	if err == nil {
		return "unknown error"
	}
	value := err.Error()
	if len(value) > 300 {
		value = value[:300]
	}
	return value
}
