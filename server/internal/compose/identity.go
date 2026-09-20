package compose

import (
	"fmt"
	"regexp"
	"strings"
)

var nativeProjectName = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,127}$`)

func normalizeNewProjectName(value string) (string, error) {
	name := strings.TrimSpace(value)
	if !nativeProjectName.MatchString(name) {
		return "", fmt.Errorf("Compose project name must be lowercase and use only letters, numbers, hyphens, or underscores, starting with a letter or number")
	}
	return name, nil
}
