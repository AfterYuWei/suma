package git

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"net/http/cgi"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestConnectionAndSyncHonorExplicitHTTPUsernameWithRealHTTPSGit(t *testing.T) {
	gitCommand, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	origin := filepath.Join(root, "repo")
	runGit(t, gitCommand, "", "init", "--initial-branch=main", origin)
	runGit(t, gitCommand, origin, "config", "user.name", "SUMA Test")
	runGit(t, gitCommand, origin, "config", "user.email", "suma@example.test")
	if err := os.WriteFile(filepath.Join(origin, "compose.yml"), []byte("services: {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, gitCommand, origin, "add", "compose.yml")
	runGit(t, gitCommand, origin, "commit", "-m", "HTTPS test")
	runGit(t, gitCommand, origin, "tag", "-a", "v1", "-m", "annotated tag")
	sha := strings.TrimSpace(runGit(t, gitCommand, origin, "rev-parse", "HEAD"))
	token := "test-only-token-never-log"
	var receivedUsername atomic.Value
	backend := &cgi.Handler{Path: gitCommand, Args: []string{"http-backend"}, Env: []string{"GIT_PROJECT_ROOT=" + root, "GIT_HTTP_EXPORT_ALL=1"}}
	remote := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		username, password, ok := r.BasicAuth()
		if !ok {
			w.Header().Set("WWW-Authenticate", `Basic realm="Git"`)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		receivedUsername.Store(username)
		if username != "repository-user" || password != token {
			http.NotFound(w, r)
			return
		}
		backend.ServeHTTP(w, r)
	}))
	defer remote.Close()
	certificate, err := x509.ParseCertificate(remote.TLS.Certificates[0].Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	ca := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificate.Raw}))
	// Direct real Git HTTPS to the fixture without changing credential handling.
	wrapper := filepath.Join(t.TempDir(), "git.sh")
	script := fmt.Sprintf(`#!/bin/sh
for arg do
 case "$arg" in https://cnb.cool/team/repo) arg=%s ;; esac
 set -- "$@" "$arg"
 shift
done
exec %s "$@"
`, strconv.Quote(remote.URL+"/repo"), strconv.Quote(gitCommand))
	if err := os.WriteFile(wrapper, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	client, err := NewCLIClient(wrapper, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	request := SyncRequest{ProjectID: 1, Repository: Repository{CloneURL: "https://cnb.cool/team/repo", RefType: RefBranch, Ref: "main", ComposeFiles: []string{"compose.yml"}}, Credential: CredentialMaterial{AuthType: AuthHTTPToken, Secret: token, CustomCA: ca}}
	// An empty username uses oauth2; a different required username is not guessed.
	request.Credential.Username = ""
	if _, err := client.TestConnection(context.Background(), request); err == nil || !strings.Contains(err.Error(), "not found") || strings.Contains(err.Error(), token) {
		t.Fatalf("wrong-user error: %v", err)
	}
	if receivedUsername.Load() != "oauth2" {
		t.Fatal("empty username did not default to oauth2")
	}
	request.Credential.Username = "repository-user"
	result, err := client.TestConnection(context.Background(), request)
	if err != nil || result.CommitSHA != sha || !result.RefChecked {
		t.Fatalf("connection: %+v, %v", result, err)
	}
	entries, err := os.ReadDir(client.root)
	if err != nil || len(entries) != 0 {
		t.Fatal("connection test created local repository data")
	}
	request.Repository.RefType, request.Repository.Ref = RefTag, "v1"
	result, err = client.TestConnection(context.Background(), request)
	if err != nil || result.CommitSHA != sha {
		t.Fatalf("annotated tag: %+v, %v", result, err)
	}
	request.Repository.Ref = "absent"
	if _, err := client.TestConnection(context.Background(), request); err == nil || !strings.Contains(err.Error(), "was not found") {
		t.Fatalf("missing tag: %v", err)
	}
	request.Repository.RefType, request.Repository.Ref = RefCommit, strings.Repeat("b", 40)
	result, err = client.TestConnection(context.Background(), request)
	if err != nil || result.RefChecked || result.CommitSHA != "" {
		t.Fatalf("unadvertised commit was incorrectly verified: %+v, %v", result, err)
	}
	request.Repository.RefType, request.Repository.Ref = RefBranch, "main"
	revision, err := client.Sync(context.Background(), request, &bytes.Buffer{})
	if err != nil || revision.CommitSHA != sha {
		t.Fatalf("sync: %+v, %v", revision, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Nanosecond)
	defer cancel()
	if _, err := client.TestConnection(ctx, request); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("deadline: %v", err)
	}
}

func TestCLIClientSyncsLocalRepositoryIntoImmutableWorktrees(t *testing.T) {
	gitCommand, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git CLI is not installed")
	}
	origin := filepath.Join(t.TempDir(), "origin")
	runGit(t, gitCommand, "", "init", "--initial-branch=main", origin)
	runGit(t, gitCommand, origin, "config", "user.name", "SUMA Test")
	runGit(t, gitCommand, origin, "config", "user.email", "suma@example.test")
	composePath := filepath.Join(origin, "compose.yml")
	if err := os.WriteFile(composePath, []byte("services:\n  app:\n    image: example/app:1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, gitCommand, origin, "add", "compose.yml")
	runGit(t, gitCommand, origin, "commit", "-m", "first release")
	firstCommit := strings.TrimSpace(runGit(t, gitCommand, origin, "rev-parse", "HEAD"))
	runGit(t, gitCommand, origin, "tag", "v1.0.0")

	// The production client intentionally rejects file:// clone URLs. This tiny
	// wrapper maps a syntactically valid HTTPS test URL to a local repository,
	// while every Git operation is still performed by the real git CLI.
	wrapper := filepath.Join(t.TempDir(), "git-wrapper.sh")
	wrapperScript := fmt.Sprintf(`#!/bin/sh
set -eu
if [ "$1" = "clone" ]; then
  exec %s clone --no-checkout --origin origin -- %s "$7"
fi
if [ "$1" = "remote" ] && [ "$2" = "set-url" ]; then
  exec %s remote set-url origin %s
fi
exec %s "$@"
`, strconv.Quote(gitCommand), strconv.Quote(origin), strconv.Quote(gitCommand), strconv.Quote(origin), strconv.Quote(gitCommand))
	if err := os.WriteFile(wrapper, []byte(wrapperScript), 0o700); err != nil {
		t.Fatal(err)
	}
	client, err := NewCLIClient(wrapper, filepath.Join(t.TempDir(), "git-data"))
	if err != nil {
		t.Fatal(err)
	}
	base := Repository{CloneURL: "https://git.example.test/team/deploy.git", ComposeFiles: []string{"compose.yml"}}

	branch := base
	branch.RefType, branch.Ref = RefBranch, "main"
	revision, err := client.Sync(context.Background(), SyncRequest{ProjectID: 1, Repository: branch, Credential: CredentialMaterial{AuthType: AuthNone}}, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if revision.CommitSHA != firstCommit || revision.CommitAuthor != "SUMA Test" || revision.CommitMessage != "first release" {
		t.Fatalf("unexpected revision: %#v", revision)
	}
	assertWorktreeFile(t, revision.WorktreePath, "example/app:1")
	firstWorktree := revision.WorktreePath

	tag := base
	tag.RefType, tag.Ref = RefTag, "v1.0.0"
	tagRevision, err := client.Sync(context.Background(), SyncRequest{ProjectID: 2, Repository: tag, Credential: CredentialMaterial{AuthType: AuthNone}}, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if tagRevision.CommitSHA != firstCommit {
		t.Fatalf("tag resolved to %q, want %q", tagRevision.CommitSHA, firstCommit)
	}

	commit := base
	commit.RefType, commit.Ref = RefCommit, firstCommit
	commitRevision, err := client.Sync(context.Background(), SyncRequest{ProjectID: 3, Repository: commit, Credential: CredentialMaterial{AuthType: AuthNone}}, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if commitRevision.CommitSHA != firstCommit {
		t.Fatalf("commit resolved to %q, want %q", commitRevision.CommitSHA, firstCommit)
	}

	if err := os.WriteFile(composePath, []byte("services:\n  app:\n    image: example/app:2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, gitCommand, origin, "add", "compose.yml")
	runGit(t, gitCommand, origin, "commit", "-m", "second release")
	secondCommit := strings.TrimSpace(runGit(t, gitCommand, origin, "rev-parse", "HEAD"))
	second, err := client.Sync(context.Background(), SyncRequest{ProjectID: 1, Repository: branch, Credential: CredentialMaterial{AuthType: AuthNone}}, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if second.CommitSHA != secondCommit || second.WorktreePath == firstWorktree {
		t.Fatalf("second revision = %#v, first worktree = %q", second, firstWorktree)
	}
	assertWorktreeFile(t, second.WorktreePath, "example/app:2")
	assertWorktreeFile(t, firstWorktree, "example/app:1")

	if err := client.Verify(context.Background(), second.WorktreePath, secondCommit); err != nil {
		t.Fatalf("Verify(clean worktree): %v", err)
	}
	if err := client.Verify(context.Background(), second.WorktreePath, firstCommit); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("Verify(wrong commit) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(second.WorktreePath, "compose.yml"), []byte("locally modified\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := client.Verify(context.Background(), second.WorktreePath, secondCommit); err == nil || !strings.Contains(err.Error(), "not immutable") {
		t.Fatalf("Verify(modified worktree) error = %v", err)
	}
	if err := client.Verify(context.Background(), t.TempDir(), secondCommit); err == nil || !strings.Contains(err.Error(), "escapes") {
		t.Fatalf("Verify(outside root) error = %v", err)
	}
	if err := client.Cleanup(1); err != nil {
		t.Fatalf("Cleanup(): %v", err)
	}
	if _, err := os.Stat(filepath.Dir(filepath.Dir(second.WorktreePath))); !os.IsNotExist(err) {
		t.Fatalf("project Git data still exists after cleanup: %v", err)
	}
}

func TestCLIClientRedactsCredentialFromOutputAndErrors(t *testing.T) {
	secretValue := "token-SHOULD-NOT-LEAK-91f44e"
	wrapper := filepath.Join(t.TempDir(), "failing-git.sh")
	script := "#!/bin/sh\nprintf 'authentication failed for %s\\n' \"$SUMA_GIT_PASSWORD\" >&2\nexit 17\n"
	if err := os.WriteFile(wrapper, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	client, err := NewCLIClient(wrapper, filepath.Join(t.TempDir(), "git-data"))
	if err != nil {
		t.Fatal(err)
	}
	repository := Repository{CloneURL: "https://git.example.test/team/deploy.git", RefType: RefBranch, Ref: "main", ComposeFiles: []string{"compose.yml"}}
	var output bytes.Buffer
	_, err = client.Sync(context.Background(), SyncRequest{ProjectID: 1, Repository: repository, Credential: CredentialMaterial{AuthType: AuthHTTPToken, Secret: secretValue}}, &output)
	if err == nil {
		t.Fatal("expected Git command failure")
	}
	for label, value := range map[string]string{"error": err.Error(), "output": output.String()} {
		if strings.Contains(value, secretValue) {
			t.Fatalf("%s leaked credential: %q", label, value)
		}
		if !strings.Contains(value, "***") {
			t.Fatalf("%s did not contain redaction marker: %q", label, value)
		}
	}
	_, err = client.TestConnection(context.Background(), SyncRequest{Repository: repository, Credential: CredentialMaterial{AuthType: AuthHTTPToken, Secret: secretValue}})
	if err == nil || strings.Contains(err.Error(), secretValue) || !strings.Contains(err.Error(), "***") {
		t.Fatalf("connection failure did not redact credential: %v", err)
	}
}

func TestNewCLIClientRejectsCompoundCommand(t *testing.T) {
	for _, command := range []string{"", "git --no-pager", "git\nstatus", "git\tstatus"} {
		if client, err := NewCLIClient(command, t.TempDir()); err == nil {
			t.Fatalf("NewCLIClient(%q) unexpectedly returned %#v", command, client)
		}
	}
}

func assertWorktreeFile(t *testing.T, root, contains string) {
	t.Helper()
	value, err := os.ReadFile(filepath.Join(root, "compose.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(value), contains) {
		t.Fatalf("compose.yml = %q, want it to contain %q", value, contains)
	}
}

func runGit(t *testing.T, command, directory string, args ...string) string {
	t.Helper()
	process := exec.Command(command, args...)
	process.Dir = directory
	output, err := process.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, output)
	}
	return string(output)
}
