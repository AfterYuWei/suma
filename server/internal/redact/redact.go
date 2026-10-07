// Package redact sanitizes untrusted diagnostics before persistence or egress.
package redact

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

var assignments = regexp.MustCompile(`(?i)((?:[a-z0-9_.-]*(?:password|passwd|token|secret|api[_-]?key|private[_-]?key|access[_-]?key|client[_-]?secret|credential|authorization|cookie)[a-z0-9_.-]*)["']?\s*[=:]\s*)("(?:\\.|[^"\\])*"|'[^'\n]*'|[^\s,;]+)`)
var bearer = regexp.MustCompile(`(?i)\b(Bearer|Basic)\s+[A-Za-z0-9+/=_\-.]+`)
var credentials = regexp.MustCompile(`(https?://)[^\s/@]+:[^\s/@]+@`)
var webhook = regexp.MustCompile(`(?i)(/bot/v2/hook/|api\.telegram\.org/bot)[A-Za-z0-9_:\-]+`)
var privateKey = regexp.MustCompile(`(?s)-----BEGIN [^-]*PRIVATE KEY-----.*?-----END [^-]*PRIVATE KEY-----`)
var standaloneKey = regexp.MustCompile(`\b(?:sk-[A-Za-z0-9_-]{16,}|(?:AKIA|ASIA)[A-Z0-9]{16}|[0-9]{8,12}:[A-Za-z0-9_-]{30,}|eyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+)\b`)

func Text(value string) string {
	value = privateKey.ReplaceAllString(value, "[REDACTED PRIVATE KEY]")
	value = bearer.ReplaceAllString(value, "${1} [REDACTED]")
	value = assignments.ReplaceAllStringFunc(value, func(match string) string {
		parts := assignments.FindStringSubmatch(match)
		if strings.Contains(parts[2], "__SUMA_SECRET_REF_") {
			return match
		}
		replacement := "[REDACTED]"
		if strings.HasPrefix(parts[2], `"`) {
			replacement = `"[REDACTED]"`
		} else if strings.HasPrefix(parts[2], "'") {
			replacement = "'[REDACTED]'"
		}
		return parts[1] + replacement
	})
	value = standaloneKey.ReplaceAllString(value, "[REDACTED]")
	value = credentials.ReplaceAllString(value, "${1}[REDACTED]@")
	return webhook.ReplaceAllString(value, "${1}[REDACTED]")
}
func Bounded(value string, limit int) string {
	if limit <= 0 {
		return ""
	}
	value = Text(strings.ToValidUTF8(value, ""))
	if len(value) <= limit {
		return value
	}
	suffix := "\n[truncated]"
	if limit < len(suffix) {
		suffix = ""
	}
	limit -= len(suffix)
	for limit > 0 && !utf8.RuneStart(value[limit]) {
		limit--
	}
	return value[:limit] + suffix
}
