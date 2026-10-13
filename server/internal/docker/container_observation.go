package docker

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	dockertypes "github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/events"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/versions"
	domain "github.com/suma/suma/server/internal/container"
	"github.com/suma/suma/server/internal/redact"
)

func parsedTime(value string) *time.Time {
	t, err := time.Parse(time.RFC3339Nano, value)
	if err != nil || t.IsZero() {
		return nil
	}
	return &t
}

// Anonymous ownership is inferred only from the Engine's creation config,
// never from a volume name that happens to look like a generated identifier.
func anonymousVolume(row dockertypes.InspectResponse, kind, destination string) *bool {
	if kind != "volume" || row.HostConfig == nil {
		return nil
	}
	var result *bool
	conflict := false
	record := func(value bool) {
		if result != nil && *result != value {
			conflict = true
		}
		result = &value
	}
	for _, mount := range row.HostConfig.Mounts {
		if string(mount.Type) == "volume" && mount.Target == destination {
			record(mount.Source == "")
		}
	}
	for _, bind := range row.HostConfig.Binds {
		parts := strings.Split(bind, ":")
		if len(parts) == 1 && parts[0] == destination {
			record(true)
		}
		if len(parts) >= 2 && parts[1] == destination {
			record(parts[0] == "")
		}
	}
	if conflict {
		return nil
	}
	if result != nil {
		return result
	}
	if len(row.HostConfig.VolumesFrom) == 0 && row.Config != nil {
		if _, declared := row.Config.Volumes[destination]; declared {
			value := true
			return &value
		}
	}
	return nil
}
func diagnosticText(value string, environment []string, limit int) (string, bool, bool) {
	safe := value
	masked := domain.MaskEnvironment(environment)
	for i, item := range environment {
		_, secret, _ := strings.Cut(item, "=")
		if masked[i].Sensitive && secret != "" && strings.Contains(safe, secret) {
			if len(secret) < 4 {
				safe = "[Sensitive diagnostic hidden]"
				break
			}
			safe = strings.ReplaceAll(safe, secret, "[REDACTED]")
		}
	}
	safe = redact.Text(safe)
	truncated := len(safe) > limit
	return redact.Bounded(safe, limit), safe != value, truncated
}
func runtimeMetadata(row dockertypes.InspectResponse, id string) domain.RuntimeMetadata {
	out := domain.RuntimeMetadata{ID: id}
	now := time.Now().UTC()
	out.ObservedAt = &now
	var env []string
	if row.Config != nil {
		env = row.Config.Env
	}
	if row.ContainerJSONBase != nil {
		count := row.RestartCount
		out.RestartCount = &count
	}
	if row.ContainerJSONBase != nil && row.HostConfig != nil {
		limit := row.HostConfig.Memory
		out.MemoryLimitBytes = &limit
	}
	if row.ContainerJSONBase != nil && row.State != nil {
		state := string(row.State.Status)
		out.State = &state
		out.StartedAt = parsedTime(row.State.StartedAt)
		out.FinishedAt = parsedTime(row.State.FinishedAt)
		if !row.State.Running && out.FinishedAt != nil {
			code := row.State.ExitCode
			out.ExitCode = &code
		}
		oom := row.State.OOMKilled
		out.OOMKilled = &oom
		text, _, _ := diagnosticText(row.State.Error, env, 2048)
		out.StateError = &text
		health := "unknown"
		if row.State.Health != nil {
			health = row.State.Health.Status
		} else if row.Config != nil && (row.Config.Healthcheck == nil || len(row.Config.Healthcheck.Test) == 0 || row.Config.Healthcheck.Test[0] == "NONE") {
			health = "none"
		}
		out.HealthStatus = &health
	}
	return out
}
func (a *Adapter) Diagnostics(ctx context.Context, id string) (domain.Diagnostics, error) {
	row, err := a.client.ContainerInspect(ctx, id)
	if err != nil {
		return domain.Diagnostics{}, err
	}
	out := domain.Diagnostics{Runtime: runtimeMetadata(row, row.ID), Health: domain.HealthDetails{Logs: []domain.HealthCheckRecord{}}}
	if row.HostConfig != nil {
		policy := string(row.HostConfig.RestartPolicy.Name)
		out.RestartPolicy = &policy
	}
	if row.Config != nil {
		cfg := row.Config.Healthcheck
		configured := cfg != nil && len(cfg.Test) > 0 && cfg.Test[0] != "NONE"
		out.Health.Configured = &configured
		if configured {
			cmd, _, _ := diagnosticText(strings.Join(cfg.Test, " "), row.Config.Env, 4096)
			out.Health.Command = &cmd
			interval, timeout, start, startInterval := cfg.Interval.Milliseconds(), cfg.Timeout.Milliseconds(), cfg.StartPeriod.Milliseconds(), cfg.StartInterval.Milliseconds()
			out.Health.IntervalMS = &interval
			out.Health.TimeoutMS = &timeout
			out.Health.StartPeriodMS = &start
			if versions.GreaterThanOrEqualTo(a.client.ClientVersion(), "1.44") {
				out.Health.StartIntervalMS = &startInterval
			}
			out.Health.Retries = &cfg.Retries
		}
	}
	if row.State != nil && row.State.Health != nil {
		h := row.State.Health
		out.Health.FailingStreak = &h.FailingStreak
		for _, log := range h.Log[max(0, len(h.Log)-5):] {
			var env []string
			if row.Config != nil {
				env = row.Config.Env
			}
			output, masked, truncated := diagnosticText(log.Output, env, 4096)
			out.Health.Logs = append(out.Health.Logs, domain.HealthCheckRecord{StartedAt: log.Start, FinishedAt: log.End, ExitCode: log.ExitCode, Output: output, OutputRedacted: masked, OutputTruncated: truncated})
			if !log.End.IsZero() {
				t := log.End
				out.Health.LastCheckedAt = &t
			}
		}
	}
	return out, nil
}
func normalizeContainerEvent(message events.Message) (domain.ContainerEvent, bool) {
	action := string(message.Action)
	health := ""
	if strings.HasPrefix(action, "health_status: ") {
		health = strings.TrimPrefix(action, "health_status: ")
		action = "health_status"
	}
	switch action {
	case "create", "start", "stop", "die", "destroy", "restart", "pause", "unpause", "kill", "health_status", "oom", "rename", "update":
	default:
		return domain.ContainerEvent{}, false
	}
	nano := message.TimeNano
	if nano == 0 {
		nano = message.Time * int64(time.Second)
	}
	out := domain.ContainerEvent{ContainerID: message.Actor.ID, Time: time.Unix(0, nano).UTC(), TimeNano: strconv.FormatInt(nano, 10), Action: action, HealthStatus: health}
	if code, err := strconv.Atoi(message.Actor.Attributes["exitCode"]); err == nil {
		out.ExitCode = &code
	}
	out.Signal = redact.Bounded(message.Actor.Attributes["signal"], 32)
	fingerprint := fmt.Sprintf("%s|%s|%s|%s|%s|%s", out.ContainerID, out.TimeNano, action, health, message.Actor.Attributes["exitCode"], out.Signal)
	sum := sha256.Sum256([]byte(fingerprint))
	out.ID = hex.EncodeToString(sum[:])
	return out, true
}
func (a *Adapter) ContainerEvents(ctx context.Context, id string, q domain.EventQuery, consume func(domain.ContainerEvent) error) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	options := events.ListOptions{Since: q.Since.Format(time.RFC3339Nano), Filters: filters.NewArgs(filters.Arg("type", "container"), filters.Arg("container", id))}
	if !q.Until.IsZero() {
		options.Until = q.Until.Format(time.RFC3339Nano)
	}
	stream, failures := a.client.Events(ctx, options)
	for stream != nil || failures != nil {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case message, ok := <-stream:
			if !ok {
				stream = nil
				continue
			}
			if message.Actor.ID != id {
				continue
			}
			if e, ok := normalizeContainerEvent(message); ok {
				if err := consume(e); err != nil {
					return err
				}
			}
		case err, ok := <-failures:
			if !ok {
				failures = nil
				continue
			}
			if err != nil {
				if err == io.EOF && !q.Until.IsZero() {
					return nil
				}
				return err
			}
		}
	}
	if q.Until.IsZero() {
		return io.EOF
	}
	return nil
}
func normalizeStats(stats dockertypes.StatsResponse) domain.Metrics {
	out := domain.Metrics{Available: true}
	if !stats.Read.IsZero() {
		t := stats.Read.UTC()
		out.SampledAt = &t
	}
	if stats.CPUStats.CPUUsage.TotalUsage >= stats.PreCPUStats.CPUUsage.TotalUsage && stats.CPUStats.SystemUsage > stats.PreCPUStats.SystemUsage {
		cpus := stats.CPUStats.OnlineCPUs
		if cpus == 0 {
			cpus = uint32(len(stats.CPUStats.CPUUsage.PercpuUsage))
		}
		out.CPUAvailable = cpus > 0
		out.CPUPercent = float64(stats.CPUStats.CPUUsage.TotalUsage-stats.PreCPUStats.CPUUsage.TotalUsage) / float64(stats.CPUStats.SystemUsage-stats.PreCPUStats.SystemUsage) * float64(cpus) * 100
	}
	out.MemoryAvailable = stats.MemoryStats.Usage > 0 || stats.MemoryStats.Limit > 0
	out.MemoryBytes = stats.MemoryStats.Usage
	cache, ok := stats.MemoryStats.Stats["total_inactive_file"]
	if !ok {
		cache = stats.MemoryStats.Stats["inactive_file"]
	}
	if cache <= out.MemoryBytes {
		out.MemoryBytes -= cache
	}
	out.NetworkAvailable = stats.Networks != nil
	out.BlockAvailable = stats.BlkioStats.IoServiceBytesRecursive != nil
	out.PIDsAvailable = stats.PidsStats.Current > 0
	for _, n := range stats.Networks {
		out.NetworkRXBytes += n.RxBytes
		out.NetworkTXBytes += n.TxBytes
	}
	for _, b := range stats.BlkioStats.IoServiceBytesRecursive {
		switch strings.ToLower(b.Op) {
		case "read":
			out.BlockReadBytes += b.Value
		case "write":
			out.BlockWriteBytes += b.Value
		}
	}
	out.PIDs = stats.PidsStats.Current
	return out
}

type normalizedStatsReader struct {
	*io.PipeReader
	cancel context.CancelFunc
	source io.ReadCloser
}

func (r *normalizedStatsReader) Close() error {
	r.cancel()
	_ = r.source.Close()
	return r.PipeReader.Close()
}
func (a *Adapter) NormalizedStats(ctx context.Context, id string) (io.ReadCloser, error) {
	ctx, cancel := context.WithCancel(ctx)
	opening := time.AfterFunc(domain.RuntimeReadTimeout, cancel)
	defer opening.Stop()
	row, err := a.client.ContainerInspect(ctx, id)
	if err != nil {
		cancel()
		return nil, err
	}
	source, err := a.Stats(ctx, id)
	if err != nil {
		cancel()
		return nil, err
	}
	if !opening.Stop() || ctx.Err() != nil {
		_ = source.Close()
		cancel()
		return nil, context.DeadlineExceeded
	}
	reader, writer := io.Pipe()
	go func() {
		defer cancel()
		defer source.Close()
		defer writer.Close()
		decoder, encoder := json.NewDecoder(source), json.NewEncoder(writer)
		for {
			var raw dockertypes.StatsResponse
			if err := decoder.Decode(&raw); err != nil {
				_ = writer.CloseWithError(err)
				return
			}
			point := normalizeStats(raw)
			point.ID = row.ID
			if row.HostConfig != nil {
				limit := row.HostConfig.Memory
				point.MemoryLimitBytes = &limit
			}
			if row.State != nil {
				point.StartedAt = parsedTime(row.State.StartedAt)
				if point.StartedAt != nil {
					point.UptimeSeconds = max(0, int64(time.Since(*point.StartedAt).Seconds()))
				}
			}
			if err := encoder.Encode(point); err != nil {
				return
			}
		}
	}()
	return &normalizedStatsReader{reader, cancel, source}, nil
}
