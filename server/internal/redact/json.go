package redact

import (
	"bytes"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
)

var ErrJSONTooLarge = errors.New("structured JSON exceeds the size limit")

var sensitiveKey = regexp.MustCompile(`(?i)(password|passwd|token|secret|api[_-]?key|private[_-]?key|access[_-]?key|client[_-]?secret|credential|authorization|cookie)`)

// JSON sanitizes a structured snapshot without changing its JSON syntax or
// numeric precision. Limits reject the document rather than truncating evidence.
func JSON(value any, limit int) ([]byte, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	if len(raw) > limit {
		return nil, ErrJSONTooLarge
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var document any
	if err := decoder.Decode(&document); err != nil {
		return nil, err
	}
	document = sanitizeJSON(document)
	raw, err = json.Marshal(document)
	if err != nil {
		return nil, err
	}
	if len(raw) > limit {
		return nil, ErrJSONTooLarge
	}
	return raw, nil
}

func sanitizeJSON(value any) any {
	switch item := value.(type) {
	case map[string]any:
		for key, child := range item {
			// Database references are evidence, not credential material.
			reference := false
			switch strings.ToLower(key) {
			case "credential_id", "credential_ids", "git_credential_id", "registry_credential_id", "registry_credential_ids", "tls_credential_id":
				reference = true
			}
			if sensitiveKey.MatchString(key) && !reference {
				item[key] = "[REDACTED]"
			} else {
				item[key] = sanitizeJSON(child)
			}
		}
	case []any:
		for i, child := range item {
			item[i] = sanitizeJSON(child)
		}
	case string:
		return Text(item)
	}
	return value
}
