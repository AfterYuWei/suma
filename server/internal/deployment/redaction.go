package deployment

import (
	"bytes"
	"encoding/json"
	"github.com/suma/suma/server/internal/compose"
	"strings"
)

// RedactConfiguration preserves structural keys and numeric types even when a
// local override is a short value such as "a" or "1". It is a display copy only.
func RedactConfiguration(raw string, environment map[string]string) (string, error) {
	var document any
	decoder := json.NewDecoder(bytes.NewBufferString(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&document); err != nil {
		return "", err
	}
	// Repository-provided and literal secrets can occur outside manual overrides.
	values := make(map[string]string, len(environment))
	for name, value := range environment {
		values[name] = value
	}
	var collect func(any, bool)
	collect = func(value any, sensitive bool) {
		switch typed := value.(type) {
		case string:
			if sensitive && typed != "" {
				values[typed] = typed
			}
			if name, text, ok := strings.Cut(typed, "="); ok && compose.IsSensitiveEnvironmentKey(name) && text != "" {
				values[text] = text
			}
		case map[string]any:
			for key, entry := range typed {
				collect(entry, sensitive || compose.IsSensitiveEnvironmentKey(key))
			}
		case []any:
			for _, entry := range typed {
				collect(entry, sensitive)
			}
		}
	}
	collect(document, false)
	var visit func(any) any
	visit = func(value any) any {
		switch typed := value.(type) {
		case string:
			return compose.RedactEnvironment(typed, values)
		case map[string]any:
			for key, entry := range typed {
				typed[key] = visit(entry)
			}
		case []any:
			for index, entry := range typed {
				typed[index] = visit(entry)
			}
		}
		return value
	}
	encoded, err := json.MarshalIndent(visit(document), "", "  ")
	return string(encoded), err
}
