package git

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

const (
	RefBranch = "branch"
	RefTag    = "tag"
	RefCommit = "commit"
)

var (
	scpURL        = regexp.MustCompile(`^(?:([^@/:]+)@)?([^/:]+):(.+)$`)
	commitSHA     = regexp.MustCompile(`^[0-9a-fA-F]{40,64}$`)
	validGitRef   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]{0,254}$`)
	validSCPHost  = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9.-]{0,251}[A-Za-z0-9])?$`)
	validSSHUser  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
	validRepoPath = regexp.MustCompile(`^[A-Za-z0-9._~/-]+$`)
)

type Repository struct {
	CloneURL        string         `json:"clone_url"`
	RefType         string         `json:"ref_type"`
	Ref             string         `json:"ref"`
	CredentialID    *uint          `json:"-"`
	Authentication  Authentication `json:"authentication"`
	ComposeFiles    []string       `json:"compose_files"`
	EnvironmentFile string         `json:"environment_file"`
}

const (
	CredentialSourceNone    = "none"
	CredentialSourceCenter  = "center"
	CredentialSourceProject = "project"
)

type Authentication struct {
	Source       string             `json:"source"`
	CredentialID *uint              `json:"credential_id,omitempty"`
	Credential   *CredentialInput   `json:"credential,omitempty"`
	Summary      *CredentialSummary `json:"summary,omitempty"`
	SaveToCenter bool               `json:"save_to_center,omitempty"`
}

type CredentialSummary struct {
	Name        string `json:"name"`
	AuthType    string `json:"auth_type"`
	Username    string `json:"username,omitempty"`
	Fingerprint string `json:"fingerprint,omitempty"`
}

func ValidateRepository(repository Repository) error {
	if len(repository.CloneURL) > 2048 {
		return errors.New("Git URL exceeds the 2048-byte limit")
	}
	if _, err := cloneHost(repository.CloneURL); err != nil {
		return err
	}
	if repository.RefType != RefBranch && repository.RefType != RefTag && repository.RefType != RefCommit {
		return errors.New("ref type must be branch, tag, or commit")
	}
	if repository.RefType == RefCommit {
		if !commitSHA.MatchString(repository.Ref) {
			return errors.New("commit ref must be a full commit SHA")
		}
	} else if !validNamedRef(repository.Ref) {
		return errors.New("invalid Git ref")
	}
	if len(repository.ComposeFiles) == 0 || len(repository.ComposeFiles) > 16 {
		return errors.New("between 1 and 16 Compose files are required")
	}
	seen := make(map[string]struct{}, len(repository.ComposeFiles))
	for _, file := range repository.ComposeFiles {
		if err := validateRelativePath(file, false); err != nil {
			return fmt.Errorf("invalid Compose file %q: %w", file, err)
		}
		if _, exists := seen[file]; exists {
			return fmt.Errorf("Compose file %q is duplicated", file)
		}
		seen[file] = struct{}{}
	}
	if repository.EnvironmentFile != "" {
		if err := validateRelativePath(repository.EnvironmentFile, false); err != nil {
			return fmt.Errorf("invalid environment file: %w", err)
		}
	}
	return nil
}

func cloneHost(value string) (string, error) {
	if strings.Contains(value, "::") || strings.HasPrefix(strings.ToLower(value), "ext:") {
		return "", errors.New("Git remote helpers are not allowed")
	}
	if strings.HasPrefix(value, "git@") || (!strings.Contains(value, "://") && scpURL.MatchString(value)) {
		match := scpURL.FindStringSubmatch(value)
		if len(match) != 4 || (match[1] != "" && !validSSHUser.MatchString(match[1])) || !validSCPHost.MatchString(match[2]) || !validRepositoryPath(match[3]) {
			return "", errors.New("invalid SSH clone URL")
		}
		return strings.ToLower(match[2]), nil
	}
	parsed, err := url.Parse(value)
	if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "ssh") || parsed.Hostname() == "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", errors.New("clone URL must use HTTPS or SSH without embedded credentials")
	}
	if parsed.User != nil {
		_, hasPassword := parsed.User.Password()
		if parsed.Scheme != "ssh" || hasPassword || !validSSHUser.MatchString(parsed.User.Username()) {
			return "", errors.New("clone URL must not contain embedded credentials")
		}
	}
	if !validRepositoryPath(strings.TrimPrefix(parsed.EscapedPath(), "/")) {
		return "", errors.New("clone URL repository path is invalid")
	}
	return strings.ToLower(parsed.Hostname()), nil
}

func CloneTransport(value string) (string, error) {
	if _, err := cloneHost(value); err != nil {
		return "", err
	}
	if strings.HasPrefix(value, "git@") || (!strings.Contains(value, "://") && scpURL.MatchString(value)) {
		return "ssh", nil
	}
	parsed, _ := url.Parse(value)
	return parsed.Scheme, nil
}

func validRepositoryPath(value string) bool {
	if value == "" || value == "/" || strings.HasPrefix(value, "/") || strings.HasPrefix(value, "-") || strings.Contains(value, "..") || !validRepoPath.MatchString(value) {
		return false
	}
	for _, part := range strings.Split(value, "/") {
		if part == "" || part == "." || part == ".." || strings.HasPrefix(part, "-") {
			return false
		}
	}
	return true
}

func validNamedRef(value string) bool {
	if !validGitRef.MatchString(value) || strings.Contains(value, "..") || strings.Contains(value, "@{") || strings.Contains(value, "//") || strings.HasSuffix(value, "/") || strings.HasSuffix(value, ".") {
		return false
	}
	for _, part := range strings.Split(value, "/") {
		if part == "" || strings.HasPrefix(part, ".") || strings.HasSuffix(part, ".lock") {
			return false
		}
	}
	return true
}

func validateRelativePath(value string, allowEmpty bool) error {
	if value == "" && allowEmpty {
		return nil
	}
	if value == "" || len(value) > 512 || strings.HasPrefix(value, "/") || strings.Contains(value, `\`) || !validRepoPath.MatchString(value) {
		return errors.New("path must be relative")
	}
	for _, part := range strings.Split(value, "/") {
		if part == "" || part == "." || part == ".." {
			return errors.New("path contains an invalid segment")
		}
	}
	return nil
}

func CanonicalRepository(value string) (string, error) {
	host, err := cloneHost(value)
	if err != nil {
		return "", err
	}
	path := repositoryPath(value)
	if path == "" {
		return "", errors.New("repository path is missing")
	}
	return strings.ToLower(host + "/" + strings.TrimSuffix(path, "/")), nil
}

func repositoryPath(value string) string {
	if match := scpURL.FindStringSubmatch(value); len(match) == 4 && !strings.Contains(value, "://") {
		return strings.TrimSuffix(strings.TrimPrefix(match[3], "/"), ".git")
	}
	parsed, err := url.Parse(value)
	if err != nil {
		return ""
	}
	return strings.TrimSuffix(strings.TrimPrefix(parsed.Path, "/"), ".git")
}
