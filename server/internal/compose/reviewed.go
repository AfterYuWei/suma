package compose

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/goccy/go-yaml"
	"github.com/suma/suma/server/internal/task"
)

type ReviewedConfig struct {
	DockerSocket bool              `json:"docker_socket"`
	TargetHash   string            `json:"target_hash"`
	Revision     string            `json:"revision,omitempty"`
	ConfigHash   string            `json:"config_hash"`
	Images       map[string]string `json:"images"`
	SourceHashes map[string]string `json:"source_hashes"`
	Services     map[string]any    `json:"services"`
}
type ImageResolver func(context.Context, string) (string, error)
type ReviewedRunner interface {
	Review(context.Context, ExecutionSpec, ImageResolver) (ReviewedConfig, error)
	ApplyReviewed(context.Context, ExecutionSpec, ReviewedConfig, int, io.Writer) error
}

func configHash(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}
func (r *CLIRunner) Review(ctx context.Context, spec ExecutionSpec, resolve ImageResolver) (ReviewedConfig, error) {
	return r.reviewConfirmed(ctx, spec, resolve, false)
}
func (r *CLIRunner) ReviewConfirmed(ctx context.Context, spec ExecutionSpec, resolve ImageResolver, allowSocket bool) (ReviewedConfig, error) {
	return r.reviewConfirmed(ctx, spec, resolve, allowSocket)
}
func (r *CLIRunner) reviewConfirmed(ctx context.Context, spec ExecutionSpec, resolve ImageResolver, allowSocket bool) (ReviewedConfig, error) {
	remote := r.target != nil && (r.target.RemoteSources || !strings.HasPrefix(r.target.Host, "unix://"))
	files := spec.Files
	if len(files) == 0 {
		files = []string{filepath.Join(spec.ProjectDir, "compose.yml")}
	}
	for _, path := range files {
		file, err := os.Open(path)
		if err != nil {
			return ReviewedConfig{}, errors.New("reviewed Compose source unavailable")
		}
		body, readErr := io.ReadAll(io.LimitReader(file, (2<<20)+1))
		file.Close()
		if readErr != nil || len(body) > 2<<20 {
			return ReviewedConfig{}, errors.New("reviewed Compose source exceeded limits")
		}
		// Check raw source before interpolation. An Agent's local proxy socket
		// must retain remote bind rules even though its transport is unix://.
		if err := ValidateComposeBindMounts(string(body), remote, allowSocket); err != nil {
			return ReviewedConfig{}, err
		}
	}
	raw, err := r.Render(ctx, spec, io.Discard)
	if err != nil {
		return ReviewedConfig{}, err
	}
	// Remote bind validation also rejects unapproved Docker socket access.
	if err := ValidateComposeBindMounts(raw, remote, allowSocket); err != nil {
		return ReviewedConfig{}, err
	}
	var cfg map[string]any
	if yaml.Unmarshal([]byte(raw), &cfg) != nil {
		return ReviewedConfig{}, errors.New("invalid rendered Compose configuration")
	}
	services, _ := cfg["services"].(map[string]any)
	if len(services) == 0 {
		return ReviewedConfig{}, errors.New("no services to review")
	}
	hashes, _, err := reviewedSources(spec.ProjectDir, cfg)
	if err != nil {
		return ReviewedConfig{}, err
	}
	review := ReviewedConfig{DockerSocket: ValidateComposeBindMounts(raw, remote, false) != nil, TargetHash: r.reviewedTargetHash(), ConfigHash: configHash(raw), Images: map[string]string{}, SourceHashes: hashes, Services: map[string]any{}}
	for name, entry := range services {
		service, ok := entry.(map[string]any)
		if !ok {
			return ReviewedConfig{}, errors.New("invalid service configuration")
		}
		image, _ := service["image"].(string)
		if image == "" {
			return ReviewedConfig{}, errors.New("reviewed deployment requires prebuilt local images for every service")
		}
		imageID, err := resolve(ctx, image)
		if err != nil {
			return ReviewedConfig{}, errors.New("deployment image is unavailable; approve a separate digest pull first")
		}
		review.Images[name] = imageID
		// Never expose environment values, command arguments, labels or secrets.
		summary := map[string]any{"image": imageID}
		for _, field := range []string{"ports", "volumes", "networks", "restart", "read_only", "privileged", "cap_add", "devices", "replicas"} {
			if value, ok := service[field]; ok {
				summary[field] = value
			}
		}
		review.Services[name] = summary
	}
	return review, nil
}
func (r *CLIRunner) ApplyReviewed(ctx context.Context, spec ExecutionSpec, review ReviewedConfig, timeout int, out io.Writer) error {
	if r.reviewedTargetHash() != review.TargetHash {
		return ErrRevisionConflict
	}
	raw, err := r.Render(ctx, spec, io.Discard)
	if err != nil {
		return err
	}
	if configHash(raw) != review.ConfigHash {
		return ErrRevisionConflict
	}
	// Execute an immutable rendered snapshot, with local image IDs fixed. The
	// private file is removed on every exit and no raw configuration is reported.
	var cfg map[string]any
	if yaml.Unmarshal([]byte(raw), &cfg) != nil {
		return errors.New("invalid rendered configuration")
	}
	hashes, sources, err := reviewedSources(spec.ProjectDir, cfg)
	if err != nil {
		return err
	}
	if configHash(jsonTextForReview(hashes)) != configHash(jsonTextForReview(review.SourceHashes)) {
		return ErrRevisionConflict
	}
	services, ok := cfg["services"].(map[string]any)
	if !ok || len(services) != len(review.Images) {
		return ErrRevisionConflict
	}
	for name, pin := range review.Images {
		if !localImageID.MatchString(pin) {
			return errors.New("reviewed images must be immutable local sha256 IDs")
		}
		service, ok := services[name].(map[string]any)
		if !ok {
			return ErrRevisionConflict
		}
		service["image"] = pin
		delete(service, "build")
		delete(service, "pull_policy")
	}
	directory, err := os.MkdirTemp("", "suma-reviewed-compose-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(directory)
	index := 0
	for section, rawEntries := range cfg {
		if section != "configs" && section != "secrets" {
			continue
		}
		entries, _ := rawEntries.(map[string]any)
		for name, entry := range entries {
			item, _ := entry.(map[string]any)
			body, ok := sources[section+"/"+name]
			if !ok {
				continue
			}
			index++
			frozenPath := filepath.Join(directory, "source-"+strconv.Itoa(index))
			if err := os.WriteFile(frozenPath, body, 0o600); err != nil {
				return err
			}
			item["file"] = frozenPath
		}
	}
	path := filepath.Join(directory, "compose.json")
	body, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	if err = os.WriteFile(path, body, 0o600); err != nil {
		return err
	}
	frozen := spec
	frozen.Files = []string{path}
	frozen.EnvFiles = nil
	args := []string{"up", "-d", "--no-build", "--pull", "never", "--wait"}
	if timeout > 0 {
		args = append(args, "--wait-timeout", strconv.Itoa(timeout))
	}
	return r.runSpec(ctx, frozen, out, args...)
}

var localImageID = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)

func (r *CLIRunner) reviewedTargetHash() string {
	if r.target == nil {
		return configHash("local-runner")
	}
	return configHash(jsonTextForReview([]any{r.target.NodeID, r.target.RuntimeIdentity, r.target.Host, r.target.RemoteSources, r.target.TLSRequired, r.target.CA, r.target.Certificate, r.target.PrivateKey, r.target.DockerConfig}))
}

func jsonTextForReview(value any) string { raw, _ := json.Marshal(value); return string(raw) }

// Freeze bounded local config/secret files too: hashing the rendered YAML alone
// would not detect a changed file, and reading it again during up would race.
func reviewedSources(projectDir string, cfg map[string]any) (map[string]string, map[string][]byte, error) {
	hashes, sources := map[string]string{}, map[string][]byte{}
	root, err := filepath.EvalSymlinks(projectDir)
	if err != nil {
		return nil, nil, err
	}
	total := 0
	for _, section := range []string{"configs", "secrets"} {
		entries, _ := cfg[section].(map[string]any)
		for name, entry := range entries {
			item, _ := entry.(map[string]any)
			if item["external"] != nil && item["external"] != false || item["environment"] != nil {
				return nil, nil, errors.New("AI reviewed deployment requires local, frozen config and secret sources")
			}
			path, _ := item["file"].(string)
			if path == "" {
				continue
			}
			if !filepath.IsAbs(path) {
				path = filepath.Join(root, path)
			}
			path, err = filepath.EvalSymlinks(path)
			if err != nil {
				return nil, nil, err
			}
			rel, err := filepath.Rel(root, path)
			if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				return nil, nil, errors.New("config/secret source escapes the project")
			}
			info, err := os.Stat(path)
			if err != nil || !info.Mode().IsRegular() || info.Size() > 2<<20 {
				return nil, nil, errors.New("invalid or oversized config/secret source")
			}
			file, err := os.Open(path)
			if err != nil {
				return nil, nil, err
			}
			body, readErr := io.ReadAll(io.LimitReader(file, (2<<20)+1))
			file.Close()
			total += len(body)
			if readErr != nil || len(body) > 2<<20 || total > 8<<20 || len(sources) >= 100 {
				return nil, nil, errors.New("config/secret source budget exceeded")
			}
			key := section + "/" + name
			hashes[key], sources[key] = configHash(string(body)), body
		}
	}
	return hashes, sources, nil
}
func (s *Service) ReviewUpdate(ctx context.Context, name string, resolve ImageResolver, allowSocket ...bool) (ReviewedConfig, error) {
	unlock := s.lockProject(name)
	defer unlock()
	project, err := s.managedProject(name)
	if err != nil {
		return ReviewedConfig{}, err
	}
	if s.operationActive(name) {
		return ReviewedConfig{}, ErrProjectBusy
	}
	runner, ok := s.runner.(ReviewedRunner)
	if !ok {
		return ReviewedConfig{}, errors.New("reviewed Compose runner unavailable")
	}
	var review ReviewedConfig
	if len(allowSocket) > 0 && allowSocket[0] {
		confirmed, ok := s.runner.(interface {
			ReviewConfirmed(context.Context, ExecutionSpec, ImageResolver, bool) (ReviewedConfig, error)
		})
		if !ok {
			return ReviewedConfig{}, errors.New("confirmed reviewed runner unavailable")
		}
		review, err = confirmed.ReviewConfirmed(ctx, ExecutionSpec{ProjectName: name, ProjectDir: project.Path}, resolve, true)
	} else {
		review, err = runner.Review(ctx, ExecutionSpec{ProjectName: name, ProjectDir: project.Path}, resolve)
	}
	review.Revision = project.Revision
	return review, err
}
func (s *Service) ApplyUpdateReviewed(ctx context.Context, name string, review ReviewedConfig, report task.Reporter) error {
	unlock := s.lockProject(name)
	defer unlock()
	if s.operationActive(name) {
		return ErrProjectBusy
	}
	project, err := s.managedProject(name)
	if err != nil {
		return err
	}
	if err = s.checkRevision(project.Path, review.Revision); err != nil {
		return err
	}
	runner, ok := s.runner.(ReviewedRunner)
	if !ok {
		return errors.New("reviewed Compose runner unavailable")
	}
	key := s.operationKey(name)
	s.operations.Store(key, true)
	defer s.operations.Delete(key)
	err = runner.ApplyReviewed(ctx, ExecutionSpec{ProjectName: name, ProjectDir: project.Path}, review, 120, io.Discard)
	if err != nil {
		return err
	}
	report(95, "Existing project configuration applied with fixed local images")
	return s.markDeployed(project)
}
