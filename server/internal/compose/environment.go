package compose

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

var environmentName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,255}$`)

func ValidateEnvironment(values map[string]string) error {
	if len(values) > 100 {
		return errors.New("at most 100 manual environment variables are allowed")
	}
	size := 0
	for name, value := range values {
		upper := strings.ToUpper(name)
		if !environmentName.MatchString(name) {
			return errors.New("invalid environment variable name")
		}
		if strings.HasPrefix(upper, "DOCKER_") || strings.HasPrefix(upper, "COMPOSE_") || upper == "HOME" || upper == "PATH" || upper == "TMPDIR" {
			return errors.New("Docker, Compose and process control environment variables are reserved")
		}
		size += len(name) + len(value)
		if strings.ContainsRune(value, 0) || len(value) > 65536 || size > 262144 {
			return errors.New("environment values contain NUL or exceed the size limit")
		}
	}
	return nil
}

// Manual values are literal dotenv values, never executable process environment
// or command arguments. Each invocation owns a short-lived private file.
func prepareEnvironment(spec ExecutionSpec) (ExecutionSpec, func(), error) {
	noop := func() {}
	if len(spec.Environment) == 0 {
		return spec, noop, nil
	}
	if err := ValidateEnvironment(spec.Environment); err != nil {
		return spec, noop, err
	}
	file, err := os.CreateTemp("", "suma-cd-environment-*.env")
	if err != nil {
		return spec, noop, errors.New("cannot create private Compose environment file")
	}
	cleanup := func() { _ = os.Remove(file.Name()) }
	names := make([]string, 0, len(spec.Environment))
	for name := range spec.Environment {
		names = append(names, name)
	}
	sort.Strings(names)
	escape := strings.NewReplacer("\\", "\\\\", "\"", "\\\"", "$", "$$", "\n", "\\n", "\r", "\\r", "\t", "\\t")
	var content strings.Builder
	for _, name := range names {
		content.WriteString(name + "=\"" + escape.Replace(spec.Environment[name]) + "\"\n")
	}
	_, writeErr := file.WriteString(content.String())
	closeErr := file.Close()
	if writeErr != nil || closeErr != nil {
		cleanup()
		return spec, noop, errors.New("cannot write private Compose environment file")
	}
	spec.EnvFiles = append([]string(nil), spec.EnvFiles...)
	if len(spec.EnvFiles) == 0 {
		implicit := filepath.Join(spec.ProjectDir, ".env")
		if _, err := os.Stat(implicit); err == nil {
			spec.EnvFiles = append(spec.EnvFiles, implicit)
		} else if !os.IsNotExist(err) {
			cleanup()
			return spec, noop, errors.New("cannot read Compose environment file")
		}
	}
	spec.EnvFiles = append(spec.EnvFiles, file.Name())
	return spec, cleanup, nil
}

func interpolationProcessEnvironment(environment []string, values map[string]string) []string {
	filtered := make([]string, 0, len(environment))
	for _, entry := range environment {
		name, _, _ := strings.Cut(entry, "=")
		if _, overridden := values[name]; !overridden {
			filtered = append(filtered, entry)
		}
	}
	return filtered
}

func RedactEnvironment(value string, environment map[string]string) string {
	var values []string
	for _, secret := range environment {
		if secret == "" {
			continue
		}
		values = append(values, secret)
		escaped, _ := json.Marshal(secret)
		values = append(values, string(escaped[1:len(escaped)-1]))
	}
	sort.Slice(values, func(i, j int) bool { return len(values[i]) > len(values[j]) })
	var replacements []string
	for _, secret := range values {
		replacements = append(replacements, secret, "[redacted]")
	}
	return strings.NewReplacer(replacements...).Replace(value)
}

// Preserve JSON and the canonical health enums needed for health assessment.
func redactRuntimeEnvironment(raw string, environment map[string]string) string {
	if len(environment) == 0 {
		return raw
	}
	var document any
	if json.Unmarshal([]byte(raw), &document) != nil {
		// Compose versions may emit one JSON object per line instead of an array.
		var rows []any
		for _, line := range strings.Split(strings.TrimSpace(raw), "\n") {
			var row any
			if json.Unmarshal([]byte(line), &row) != nil {
				return ""
			}
			rows = append(rows, row)
		}
		document = rows
	}
	var visit func(any) any
	visit = func(value any) any {
		switch typed := value.(type) {
		case string:
			return RedactEnvironment(typed, environment)
		case []any:
			for i := range typed {
				typed[i] = visit(typed[i])
			}
		case map[string]any:
			for key, child := range typed {
				if (key == "State" || key == "Health") && (child == "running" || child == "healthy" || child == "unhealthy" || child == "starting" || child == "exited" || child == "") {
					continue
				}
				typed[key] = visit(child)
			}
		}
		return value
	}
	result, _ := json.Marshal(visit(document))
	return string(result)
}
