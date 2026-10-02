package compose

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/goccy/go-yaml"
)

var ErrRevisionConflict = errors.New("Project configuration changed; compare or reload the saved configuration")
var ErrProjectBusy = errors.New("Project operation is in progress; wait before changing configuration")

func configurationRevision(compose, environment string) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%d:%s%d:%s", len(compose), compose, len(environment), environment)))
	return hex.EncodeToString(sum[:])
}
func readConfiguration(path string) ([2]string, error) {
	var result [2]string
	for i, name := range []string{"compose.yml", ".env"} {
		value, err := os.ReadFile(filepath.Join(path, name))
		if err != nil && !(i == 1 && errors.Is(err, os.ErrNotExist)) {
			return result, err
		}
		result[i] = string(value)
	}
	return result, nil
}
func (s *Service) checkRevision(path, expected string) error {
	if expected == "" {
		return nil
	}
	content, err := readConfiguration(path)
	if err != nil {
		return err
	}
	if configurationRevision(content[0], content[1]) != expected {
		return ErrRevisionConflict
	}
	return nil
}
func (s *Service) operationKey(name string) string { return s.effectiveNodeID() + "\x00" + name }
func (s *Service) operationActive(name string) bool {
	if s.operations == nil {
		return false
	}
	_, active := s.operations.Load(s.operationKey(name))
	return active
}

func validateConfigurationIdentity(name, content, environment string) error {
	var document map[string]any
	if err := yaml.Unmarshal([]byte(content), &document); err != nil {
		return errors.New("Compose YAML cannot be parsed")
	}
	return validateTakeoverProjectName(name, content, environment)
}

// Prepare both files before publishing either. Callers hold the Project lock,
// so readers see a single configuration and write failures restore the old pair.
func writeConfiguration(path, content, environment string) error {
	return publishConfiguration(path, content, environment, os.Rename)
}

func publishConfiguration(path, content, environment string, rename func(string, string) error) error {
	names := []string{"compose.yml", ".env"}
	values := []string{content, environment}
	var previous [2][]byte
	var existed [2]bool
	var staged [2]string
	defer func() {
		for _, name := range staged {
			if name != "" {
				_ = os.Remove(name)
			}
		}
	}()
	for i, name := range names {
		value, err := os.ReadFile(filepath.Join(path, name))
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		previous[i], existed[i] = value, err == nil
		file, err := os.CreateTemp(path, ".configuration-")
		if err != nil {
			return err
		}
		staged[i] = file.Name()
		if err = file.Chmod(0o640); err == nil {
			_, err = file.WriteString(values[i])
		}
		if err == nil {
			err = file.Sync()
		}
		closeErr := file.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
	}
	for i, name := range names {
		if err := rename(staged[i], filepath.Join(path, name)); err != nil {
			for j := 0; j < i; j++ {
				if existed[j] {
					if rollbackErr := writeAtomic(filepath.Join(path, names[j]), string(previous[j])); rollbackErr != nil {
						return errors.Join(err, rollbackErr)
					}
				} else {
					_ = os.Remove(filepath.Join(path, names[j]))
				}
			}
			return err
		}
	}
	return nil
}

// ValidateConfiguration uses the actual managed directory for relative paths,
// with candidate files private to a short-lived directory below the Compose root.
func (s *Service) ValidateConfiguration(ctx context.Context, name, content, environment string) error {
	if _, err := normalizeNewProjectName(name); err != nil {
		return err
	}
	if err := validateConfigurationIdentity(name, content, environment); err != nil {
		return err
	}
	if err := validateManagedTakeoverContent(content); err != nil {
		return errors.New("Compose must declare services and use supported managed file resources")
	}
	if s.runner == nil {
		return errors.New("Compose runner is unavailable")
	}
	path, err := s.safePath(name)
	if err != nil {
		return err
	}
	temporary, err := os.MkdirTemp(s.root, ".configuration-validate-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(temporary)
	if err := writeConfiguration(temporary, content, environment); err != nil {
		return err
	}
	var output diagnosticWriter
	if runner, ok := s.runner.(interface {
		ValidateRelease(context.Context, ExecutionSpec, io.Writer) error
	}); ok {
		err = runner.ValidateRelease(ctx, ExecutionSpec{ProjectName: name, ProjectDir: path, Files: []string{filepath.Join(temporary, "compose.yml")}, EnvFiles: []string{filepath.Join(temporary, ".env")}}, &output)
	} else {
		err = s.runner.Validate(ctx, temporary, &output)
	}
	if err != nil {
		// Compose errors may include inline secrets. Include only diagnostics when
		// the candidate has no sensitive keys; never include candidate source.
		if hasSensitiveConfiguration(content) || hasSensitiveEnvironment(environment) {
			return errors.New("Compose validation failed; check configuration fields (sensitive diagnostic hidden)")
		}
		if detail := composeValidationDetail(output.value, environment); detail != "" {
			return fmt.Errorf("Compose validation failed: %s", detail)
		}
		return errors.New("Compose validation failed")
	}
	return nil
}

func hasSensitiveEnvironment(environment string) bool {
	for _, line := range strings.Split(environment, "\n") {
		key, _, ok := strings.Cut(strings.TrimSpace(line), "=")
		if ok && sensitiveEnvironmentKey(strings.TrimSpace(strings.TrimPrefix(key, "export "))) {
			return true
		}
	}
	return false
}

// CLI output is streamed into persistent Tasks. Redact candidate credentials
// before either live progress or historical logs receive a line.
func configurationRedactor(content, environment string) func(string) string {
	var document any
	if yaml.Unmarshal([]byte(content), &document) != nil {
		return func(string) string { return "Compose diagnostic hidden; check configuration syntax" }
	}
	values := map[string]bool{}
	complexInline := false
	add := func(value string) {
		for _, part := range strings.Split(value, "\n") {
			part = strings.TrimSpace(part)
			if part != "" {
				values[part] = true
			}
		}
	}
	var visit func(any, bool)
	visit = func(value any, sensitive bool) {
		switch v := value.(type) {
		case string:
			if sensitive {
				complexInline = complexInline || strings.Contains(v, "$")
				add(v)
			}
			if key, text, ok := strings.Cut(v, "="); ok && sensitiveEnvironmentKey(key) {
				complexInline = complexInline || strings.Contains(text, "$")
				add(text)
			}
		case int, int64, uint64, float64, bool:
			if sensitive {
				add(fmt.Sprint(v))
			}
		case map[string]any:
			for key, child := range v {
				visit(child, sensitive || sensitiveEnvironmentKey(key))
			}
		case []any:
			for _, child := range v {
				visit(child, sensitive)
			}
		}
	}
	visit(document, false)
	if complexInline {
		return func(string) string {
			return "Compose output hidden because complex sensitive environment configuration is present"
		}
	}
	for _, line := range strings.Split(environment, "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok || !sensitiveEnvironmentKey(strings.TrimSpace(strings.TrimPrefix(key, "export "))) {
			continue
		}
		value = strings.TrimSpace(value)
		complex := strings.ContainsAny(value, "$\\")
		if len(value) > 0 && (value[0] == '\'' || value[0] == '"') {
			end := strings.IndexByte(value[1:], value[0])
			if end < 0 {
				complex = true
			} else {
				value = value[1 : end+1]
			}
		} else if i := strings.Index(value, " #"); i >= 0 {
			value = strings.TrimSpace(value[:i])
		}
		if complex {
			return func(string) string {
				return "Compose output hidden because complex sensitive environment configuration is present"
			}
		}
		add(value)
	}
	secrets := make([]string, 0, len(values))
	for value := range values {
		secrets = append(secrets, value)
	}
	sort.Slice(secrets, func(i, j int) bool { return len(secrets[i]) > len(secrets[j]) })
	return func(message string) string {
		for _, secret := range secrets {
			if len([]rune(secret)) < 4 && strings.Contains(message, secret) {
				return "Compose diagnostic hidden because it contains a short sensitive value"
			}
			message = strings.ReplaceAll(message, secret, "***")
		}
		return message
	}
}

type diagnosticWriter struct{ value string }

func (w *diagnosticWriter) Write(value []byte) (int, error) {
	if len(w.value) < 8192 {
		w.value += string(value)
	}
	return len(value), nil
}

var _ io.Writer = (*diagnosticWriter)(nil)

func hasSensitiveConfiguration(content string) bool {
	var document any
	if yaml.Unmarshal([]byte(content), &document) != nil {
		return true
	}
	var visit func(any) bool
	visit = func(value any) bool {
		switch v := value.(type) {
		case map[string]any:
			for key, child := range v {
				if sensitiveEnvironmentKey(key) {
					return true
				}
				if visit(child) {
					return true
				}
			}
		case []any:
			for _, child := range v {
				if text, ok := child.(string); ok {
					for i, c := range text {
						if c == '=' {
							if sensitiveEnvironmentKey(text[:i]) {
								return true
							}
							break
						}
					}
				}
				if visit(child) {
					return true
				}
			}
		}
		return false
	}
	return visit(document)
}
