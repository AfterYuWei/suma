package git

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type Revision struct {
	CommitSHA     string `json:"commit_sha"`
	CommitAuthor  string `json:"commit_author"`
	CommitMessage string `json:"commit_message"`
	WorktreePath  string `json:"-"`
}

type SyncRequest struct {
	ProjectID  uint
	Repository Repository
	Credential CredentialMaterial
}

type Client interface {
	TestConnection(context.Context, SyncRequest) (ConnectionResult, error)
	Sync(context.Context, SyncRequest, io.Writer) (Revision, error)
	Verify(context.Context, string, string) error
	Cleanup(uint) error
}

type CLIClient struct {
	command string
	root    string
}

func NewCLIClient(command, root string) (*CLIClient, error) {
	if strings.TrimSpace(command) == "" || strings.ContainsAny(command, " \t\r\n") {
		return nil, errors.New("Git command must be one executable path")
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(absolute, 0o750); err != nil {
		return nil, fmt.Errorf("create Git data root: %w", err)
	}
	return &CLIClient{command: command, root: absolute}, nil
}

type ConnectionResult struct {
	CommitSHA  string `json:"commit_sha,omitempty"`
	RefChecked bool   `json:"ref_checked"`
}

// TestConnection reads remote refs only: no clone, worktree or deployment.
// Arbitrary commits may be unadvertised, so they are verified during sync.
func (c *CLIClient) TestConnection(ctx context.Context, request SyncRequest) (ConnectionResult, error) {
	if err := ValidateRemote(request.Repository); err != nil {
		return ConnectionResult{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	environment, cleanup, err := authenticationEnvironment(request.Credential)
	if err != nil {
		return ConnectionResult{}, err
	}
	defer cleanup()
	reference := "HEAD"
	switch request.Repository.RefType {
	case RefBranch:
		reference = "refs/heads/" + request.Repository.Ref
	case RefTag:
		reference = "refs/tags/" + request.Repository.Ref
	}
	args := []string{"ls-remote", "--", request.Repository.CloneURL, reference}
	if request.Repository.RefType == RefTag {
		args = append(args, reference+"^{}")
	}
	output, err := c.capture(ctx, "", environment, []string{request.Credential.Secret, request.Credential.Passphrase, request.Credential.PrivateKey}, args...)
	if err != nil {
		return ConnectionResult{}, err
	}
	if request.Repository.RefType == RefCommit {
		return ConnectionResult{RefChecked: false}, nil
	}
	result := ConnectionResult{RefChecked: true}
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && commitSHA.MatchString(fields[0]) && (fields[1] == reference || fields[1] == reference+"^{}") {
			// Prefer the commit behind an annotated tag over its tag object.
			if result.CommitSHA == "" || fields[1] == reference+"^{}" {
				result.CommitSHA = fields[0]
			}
		}
	}
	if result.CommitSHA == "" {
		return ConnectionResult{}, fmt.Errorf("remote %s %q was not found; check the reference name and credential access", request.Repository.RefType, request.Repository.Ref)
	}
	return result, nil
}

func (c *CLIClient) Sync(ctx context.Context, request SyncRequest, output io.Writer) (Revision, error) {
	if err := ValidateRepository(request.Repository); err != nil {
		return Revision{}, err
	}
	projectRoot := filepath.Join(c.root, strconv.FormatUint(uint64(request.ProjectID), 10))
	repositoryPath := filepath.Join(projectRoot, "repository")
	worktreesRoot := filepath.Join(projectRoot, "worktrees")
	if err := os.MkdirAll(worktreesRoot, 0o750); err != nil {
		return Revision{}, fmt.Errorf("create Git project directory: %w", err)
	}
	environment, cleanup, err := authenticationEnvironment(request.Credential)
	if err != nil {
		return Revision{}, err
	}
	defer cleanup()
	redactions := []string{request.Credential.Secret, request.Credential.Passphrase, request.Credential.PrivateKey}
	if _, err := os.Stat(filepath.Join(repositoryPath, ".git")); errors.Is(err, os.ErrNotExist) {
		if err := c.run(ctx, "", environment, output, redactions, "clone", "--no-checkout", "--origin", "origin", "--", request.Repository.CloneURL, repositoryPath); err != nil {
			_ = os.RemoveAll(repositoryPath)
			return Revision{}, err
		}
	} else if err != nil {
		return Revision{}, fmt.Errorf("inspect Git repository: %w", err)
	}
	if err := c.run(ctx, repositoryPath, environment, output, redactions, "remote", "set-url", "origin", request.Repository.CloneURL); err != nil {
		return Revision{}, err
	}
	if err := c.run(ctx, repositoryPath, environment, output, redactions, "fetch", "--prune", "--tags", "origin"); err != nil {
		return Revision{}, err
	}
	reference := request.Repository.Ref
	switch request.Repository.RefType {
	case RefBranch:
		reference = "refs/remotes/origin/" + request.Repository.Ref
	case RefTag:
		reference = "refs/tags/" + request.Repository.Ref
	}
	commit, err := c.capture(ctx, repositoryPath, environment, redactions, "rev-parse", "--verify", reference+"^{commit}")
	if err != nil {
		return Revision{}, err
	}
	commit = strings.TrimSpace(commit)
	if !commitSHA.MatchString(commit) {
		return Revision{}, errors.New("Git returned an invalid commit SHA")
	}
	worktreePath := filepath.Join(worktreesRoot, strings.ToLower(commit))
	if _, err := os.Stat(worktreePath); errors.Is(err, os.ErrNotExist) {
		if err := c.run(ctx, repositoryPath, environment, output, redactions, "worktree", "add", "--detach", worktreePath, commit); err != nil {
			return Revision{}, err
		}
	} else if err != nil {
		return Revision{}, fmt.Errorf("inspect Git worktree: %w", err)
	}
	metadata, err := c.capture(ctx, repositoryPath, environment, redactions, "show", "-s", "--format=%H%n%an%n%s", commit)
	if err != nil {
		return Revision{}, err
	}
	lines := strings.SplitN(strings.TrimSpace(metadata), "\n", 3)
	revision := Revision{CommitSHA: commit, WorktreePath: worktreePath}
	if len(lines) > 1 {
		revision.CommitAuthor = lines[1]
	}
	if len(lines) > 2 {
		revision.CommitMessage = lines[2]
	}
	return revision, nil
}

func (c *CLIClient) Verify(ctx context.Context, worktree, commit string) error {
	if !commitSHA.MatchString(commit) {
		return errors.New("invalid release commit SHA")
	}
	resolved, err := filepath.EvalSymlinks(worktree)
	if err != nil {
		return fmt.Errorf("resolve release worktree: %w", err)
	}
	if err := pathBelow(c.root, resolved); err != nil {
		return err
	}
	head, err := c.capture(ctx, resolved, baseGitEnvironment(), nil, "rev-parse", "HEAD")
	if err != nil {
		return err
	}
	if !strings.EqualFold(strings.TrimSpace(head), commit) {
		return errors.New("release worktree commit does not match the recorded release")
	}
	status, err := c.capture(ctx, resolved, baseGitEnvironment(), nil, "status", "--porcelain=v1", "--untracked-files=all")
	if err != nil {
		return err
	}
	ignored, err := c.capture(ctx, resolved, baseGitEnvironment(), nil, "clean", "-ndx")
	if err != nil {
		return err
	}
	if strings.TrimSpace(status) != "" || strings.TrimSpace(ignored) != "" {
		return errors.New("release worktree is not immutable: local changes or untracked files were found")
	}
	return nil
}

func (c *CLIClient) Cleanup(projectID uint) error {
	projectRoot := filepath.Join(c.root, strconv.FormatUint(uint64(projectID), 10))
	if err := pathBelow(c.root, projectRoot); err != nil {
		return err
	}
	return os.RemoveAll(projectRoot)
}

func (c *CLIClient) run(ctx context.Context, directory string, environment []string, output io.Writer, redactions []string, args ...string) error {
	var buffer bytes.Buffer
	command := exec.CommandContext(ctx, c.command, args...)
	command.WaitDelay = time.Second
	command.Dir = directory
	command.Env = append(safeGitEnvironment(), environment...)
	command.Stdout = &buffer
	command.Stderr = &buffer
	err := command.Run()
	message := redact(buffer.String(), redactions)
	if message != "" && output != nil {
		_, _ = io.WriteString(output, message)
	}
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("git %s failed: %s", args[0], strings.TrimSpace(message))
	}
	return nil
}

func (c *CLIClient) capture(ctx context.Context, directory string, environment []string, redactions []string, args ...string) (string, error) {
	var output bytes.Buffer
	command := exec.CommandContext(ctx, c.command, args...)
	command.WaitDelay = time.Second
	command.Dir = directory
	command.Env = append(safeGitEnvironment(), environment...)
	command.Stdout = &output
	command.Stderr = &output
	if err := command.Run(); err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", fmt.Errorf("git %s failed: %s", args[0], strings.TrimSpace(redact(output.String(), redactions)))
	}
	return output.String(), nil
}

func authenticationEnvironment(credential CredentialMaterial) ([]string, func(), error) {
	directory, err := os.MkdirTemp("", "suma-git-")
	if err != nil {
		return nil, func() {}, err
	}
	cleanup := func() { _ = os.RemoveAll(directory) }
	environment := baseGitEnvironment()
	if credential.CustomCA != "" {
		path := filepath.Join(directory, "ca.pem")
		if err := os.WriteFile(path, []byte(credential.CustomCA), 0o600); err != nil {
			cleanup()
			return nil, func() {}, err
		}
		environment = append(environment, "GIT_SSL_CAINFO="+path)
	}
	switch credential.AuthType {
	case "", AuthNone:
	case AuthHTTPToken, AuthHTTPBasic:
		username := credential.Username
		if username == "" {
			username = "oauth2"
		}
		taskPass := filepath.Join(directory, "askpass.sh")
		script := "#!/bin/sh\ncase \"$1\" in\n  *Username*) printf '%s' \"$SUMA_GIT_USERNAME\" ;;\n  *) printf '%s' \"$SUMA_GIT_PASSWORD\" ;;\nesac\n"
		if err := os.WriteFile(taskPass, []byte(script), 0o700); err != nil {
			cleanup()
			return nil, func() {}, err
		}
		environment = append(environment, "GIT_ASKPASS="+taskPass, "SUMA_GIT_USERNAME="+username, "SUMA_GIT_PASSWORD="+credential.Secret)
	case AuthSSHKey:
		if credential.PrivateKey == "" || credential.KnownHosts == "" {
			cleanup()
			return nil, func() {}, errors.New("SSH private key and known_hosts are required")
		}
		keyPath := filepath.Join(directory, "identity")
		hostsPath := filepath.Join(directory, "known_hosts")
		if err := os.WriteFile(keyPath, []byte(credential.PrivateKey), 0o600); err != nil {
			cleanup()
			return nil, func() {}, err
		}
		if err := os.WriteFile(hostsPath, []byte(credential.KnownHosts), 0o600); err != nil {
			cleanup()
			return nil, func() {}, err
		}
		sshCommand := "ssh -F /dev/null -i " + keyPath + " -o IdentitiesOnly=yes -o PreferredAuthentications=publickey -o PasswordAuthentication=no -o KbdInteractiveAuthentication=no -o StrictHostKeyChecking=yes -o UserKnownHostsFile=" + hostsPath
		environment = append(environment, "GIT_SSH_COMMAND="+sshCommand)
		if credential.Passphrase != "" {
			taskPass := filepath.Join(directory, "ssh-askpass.sh")
			if err := os.WriteFile(taskPass, []byte("#!/bin/sh\nprintf '%s' \"$SUMA_GIT_PASSPHRASE\"\n"), 0o700); err != nil {
				cleanup()
				return nil, func() {}, err
			}
			environment = append(environment, "SSH_ASKPASS="+taskPass, "SSH_ASKPASS_REQUIRE=force", "SUMA_GIT_PASSPHRASE="+credential.Passphrase, "DISPLAY=suma:0")
		}
	default:
		cleanup()
		return nil, func() {}, fmt.Errorf("unsupported credential type %q", credential.AuthType)
	}
	return environment, cleanup, nil
}

func safeGitEnvironment() []string {
	allowed := []string{"PATH", "HOME", "TMPDIR", "LANG", "LC_ALL", "HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY", "http_proxy", "https_proxy", "no_proxy"}
	values := make([]string, 0, len(allowed))
	for _, key := range allowed {
		if value, ok := os.LookupEnv(key); ok {
			values = append(values, key+"="+value)
		}
	}
	return values
}

func baseGitEnvironment() []string {
	// file is needed for Git's own local repository/worktree plumbing. User
	// supplied clone URLs are still restricted to HTTPS and SSH before Git runs.
	return []string{"GIT_TERMINAL_PROMPT=0", "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_ALLOW_PROTOCOL=https:ssh:file", "LC_ALL=C"}
}

func redact(value string, secrets []string) string {
	for _, secret := range secrets {
		if secret != "" {
			value = strings.ReplaceAll(value, secret, "***")
		}
	}
	return value
}

func pathBelow(root, value string) error {
	relative, err := filepath.Rel(root, value)
	if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return errors.New("Git path escapes the configured data root")
	}
	return nil
}
