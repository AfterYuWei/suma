package compose

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestAgentComposeDraftPreservesSecretsAndRequiresSeparateSave(t *testing.T) {
	runner := &agentDraftRunner{}
	s := configurationService(t, runner)
	ctx := context.Background()
	original := "services:\n  app:\n    image: nginx:stable\n    environment:\n      PASSWORD: private-value\n      PORT: '8080'\n"
	project, err := s.Create(ctx, "app", original, "TOKEN=private-env\n")
	if err != nil {
		t.Fatal(err)
	}
	safe, err := AgentConfig(project.Compose)
	if err != nil || strings.Contains(safe, "private-value") || !strings.Contains(safe, secretRefPrefix) {
		t.Fatal("model received a secret", safe, err)
	}
	proposed := strings.ReplaceAll(safe, "nginx:stable", "nginx:alpine")
	content, revision, preview, err := s.PrepareAgentDraft(ctx, "app", proposed, project.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(content, "private-value") || strings.Contains(preview, "private-value") || strings.Contains(preview, "private-env") || runner.deployed {
		t.Fatal("draft changed secrets, leaked values or deployed")
	}
	unchanged, _ := s.Get(ctx, "app")
	if unchanged.Revision != project.Revision {
		t.Fatal("draft saved configuration before approval")
	}
	saved, err := s.SaveAgentDraft(ctx, "app", content, revision, false)
	if err != nil || saved.Environment != project.Environment || runner.deployed {
		t.Fatal("save changed environment or deployed", err)
	}
	if _, _, _, err = s.PrepareAgentDraft(ctx, "app", proposed, revision); !errors.Is(err, ErrRevisionConflict) {
		t.Fatal("stale draft accepted", err)
	}
}
func TestAgentComposeSecretReferencesCannotBeMovedOrRenamed(t *testing.T) {
	s := configurationService(t, &agentDraftRunner{})
	ctx := context.Background()
	project, err := s.Create(ctx, "app", "services:\n  app:\n    image: nginx\n    environment:\n      - PASSWORD=private-value\n", "")
	if err != nil {
		t.Fatal(err)
	}
	safe, _ := AgentConfig(project.Compose)
	for _, input := range []string{strings.ReplaceAll(safe, "PASSWORD=", "PUBLIC="), strings.ReplaceAll(safe, "app:", "other:"), strings.ReplaceAll(safe, secretRefPrefix, "NEW_SECRET_")} {
		if _, _, _, err := s.PrepareAgentDraft(ctx, "app", input, project.Revision); err == nil {
			t.Fatal("model moved or modified a protected credential")
		}
	}
}
func TestAgentComposeDraftKeepsRemoteBindRulesAndSocketConfirmation(t *testing.T) {
	s := configurationService(t, &agentDraftRunner{})
	s.localSources = false
	ctx := context.Background()
	for _, source := range []string{"./relative", "${HOST_PATH}"} {
		content := "services:\n  app:\n    image: nginx\n    volumes:\n      - '" + source + ":/app'\n"
		if _, _, _, err := s.PrepareAgentDraft(ctx, "app", content, ""); err == nil {
			t.Fatal("remote bind source accepted", source)
		}
	}
	content := "services:\n  app:\n    image: nginx\n    volumes:\n      - /var/run/docker.sock:/var/run/docker.sock:ro\n"
	_, _, preview, err := s.PrepareAgentDraft(ctx, "app", content, "")
	if err != nil || !strings.Contains(preview, `"docker_socket":true`) {
		t.Fatal("socket risk omitted", preview, err)
	}
}

type agentDraftRunner struct{ takeoverRunner }

func (r *agentDraftRunner) ValidateRelease(ctx context.Context, _ ExecutionSpec, out io.Writer) error {
	return r.Validate(ctx, "", out)
}

func TestAgentSourcesCannotReadOutsideProject(t *testing.T) {
	s := configurationService(t, &agentDraftRunner{})
	for _, source := range []string{"build: /etc", "build: ../other", "env_file: /etc/passwd", "build: https://example.com/source.git", "extends: {file: ../other.yml}", "build: {context: ., dockerfile: ../../Dockerfile}"} {
		content := "services:\n  app:\n    image: nginx\n    " + source + "\n"
		if err := s.ValidateAgentSources("app", content); err == nil {
			t.Fatal("source outside Project accepted", source)
		}
	}
	if err := s.ValidateAgentSources("app", "services:\n  app:\n    build: {context: ., dockerfile: Dockerfile}\n    env_file: .env\n"); err != nil {
		t.Fatal(err)
	}
}
