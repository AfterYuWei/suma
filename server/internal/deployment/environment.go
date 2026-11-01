package deployment

import (
	"encoding/json"
	"errors"
	"github.com/suma/suma/server/internal/compose"
	"github.com/suma/suma/server/internal/secret"
	"sort"
)

// Configuration reads omit Value. A missing value preserves the stored value for Name;
// a pointer to an empty string explicitly sets an empty interpolation value.
type EnvironmentVariable struct {
	Name     string  `json:"name"`
	Value    *string `json:"value,omitempty"`
	HasValue bool    `json:"has_value,omitempty"`
}

func DecryptEnvironment(store *secret.Store, ciphertext []byte) (map[string]string, error) {
	values := map[string]string{}
	if len(ciphertext) == 0 {
		return values, nil
	}
	raw, err := store.Decrypt(ciphertext)
	if err != nil {
		return nil, errors.New("cannot decrypt delivery environment")
	}
	if json.Unmarshal([]byte(raw), &values) != nil {
		return nil, errors.New("cannot decode delivery environment")
	}
	return values, nil
}

func ConfigureEnvironment(store *secret.Store, current []byte, input []EnvironmentVariable) ([]byte, error) {
	// Existing clients preserve manual values.
	if input == nil {
		return current, nil
	}
	if len(input) > 100 {
		return nil, errors.New("at most 100 manual environment variables are allowed")
	}
	stored, err := DecryptEnvironment(store, current)
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
	return store.Encrypt(string(raw))
}

func EnvironmentSummary(store *secret.Store, ciphertext []byte) ([]EnvironmentVariable, error) {
	values, err := DecryptEnvironment(store, ciphertext)
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
