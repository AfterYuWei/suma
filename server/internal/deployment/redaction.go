package deployment

import (
	"bytes"
	"encoding/json"
	"github.com/suma/suma/server/internal/compose"
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
	var visit func(any) any
	visit = func(value any) any {
		switch typed := value.(type) {
		case string:
			return compose.RedactEnvironment(typed, environment)
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
