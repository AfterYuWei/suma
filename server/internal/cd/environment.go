package cd

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"

	"github.com/suma/suma/server/internal/compose"
	"github.com/suma/suma/server/internal/database"
)

// Configuration reads omit Value. A missing value preserves the stored value for Name;
// a pointer to an empty string explicitly sets an empty interpolation value.
type EnvironmentVariable struct {
	Name     string  `json:"name"`
	Value    *string `json:"value,omitempty"`
	HasValue bool    `json:"has_value,omitempty"`
}

func (s *Service) GetEnvironmentValues(ctx context.Context, name string) (map[string]string, error) {
	project, err := s.project(ctx, name)
	if err != nil {
		return nil, err
	}
	return s.decryptEnvironment(project.EnvironmentCiphertext)
}

func (s *Service) decryptEnvironment(ciphertext []byte) (map[string]string, error) {
	values := map[string]string{}
	if len(ciphertext) == 0 {
		return values, nil
	}
	raw, err := s.secrets.Decrypt(ciphertext)
	if err != nil {
		return nil, errors.New("cannot decrypt delivery environment")
	}
	if json.Unmarshal([]byte(raw), &values) != nil {
		return nil, errors.New("cannot decode delivery environment")
	}
	return values, nil
}

func (s *Service) configureEnvironment(current []byte, input []EnvironmentVariable) ([]byte, error) {
	// Existing clients preserve manual values.
	if input == nil {
		return current, nil
	}
	if len(input) > 100 {
		return nil, errors.New("at most 100 manual environment variables are allowed")
	}
	stored, err := s.decryptEnvironment(current)
	if err != nil {
		return nil, err
	}
	values := map[string]string{}
	for _, variable := range input {
		if _, exists := values[variable.Name]; exists {
			return nil, errors.New("duplicate environment variable name")
		}
		if variable.Value != nil {
			values[variable.Name] = *variable.Value
		} else if value, exists := stored[variable.Name]; exists {
			values[variable.Name] = value
		} else {
			return nil, errors.New("a value is required for a new environment variable")
		}
	}
	if err := compose.ValidateEnvironment(values); err != nil {
		return nil, err
	}
	raw, _ := json.Marshal(values)
	previous, _ := json.Marshal(stored)
	if string(raw) == string(previous) {
		return current, nil
	}
	if len(values) == 0 {
		return nil, nil
	}
	return s.secrets.Encrypt(string(raw))
}

func (s *Service) environmentSummary(ciphertext []byte) ([]EnvironmentVariable, error) {
	values, err := s.decryptEnvironment(ciphertext)
	if err != nil {
		return nil, err
	}
	rows := make([]EnvironmentVariable, 0, len(values))
	for name := range values {
		rows = append(rows, EnvironmentVariable{Name: name, HasValue: true})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Name < rows[j].Name })
	return rows, nil
}

func (s *Service) releaseSpec(projectName string, release database.DeliveryRelease) (compose.ExecutionSpec, error) {
	spec, err := releaseExecutionSpec(projectName, release)
	if err != nil {
		return spec, err
	}
	spec.Environment, err = s.decryptEnvironment(release.EnvironmentCiphertext)
	return spec, err
}

// Include even unused manual values so changing them creates an independently
// reviewable candidate, while retaining the hash of pre-upgrade releases.
func releaseConfigurationHash(rendered string, environment map[string]string) string {
	if len(environment) > 0 {
		values, _ := json.Marshal(environment)
		rendered += "\x00" + string(values)
	}
	hash := sha256.Sum256([]byte(rendered))
	return hex.EncodeToString(hash[:])
}
