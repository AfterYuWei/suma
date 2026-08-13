package git

import (
	"strings"
	"testing"
)

func TestValidateRepositoryURLs(t *testing.T) {
	tests := []struct {
		name       string
		repository Repository
		valid      bool
	}{
		{"HTTPS", Repository{CloneURL: "https://forge.example/team/deploy.git", RefType: RefBranch, Ref: "main", ComposeFiles: []string{"compose/base.yml", "environments/production.yml"}}, true},
		{"SSH", Repository{CloneURL: "ssh://git@forge.example:2222/team/deploy.git", RefType: RefTag, Ref: "v1.0.0", ComposeFiles: []string{"compose.yml"}}, true},
		{"SCP", Repository{CloneURL: "git@forge.example:team/deploy.git", RefType: RefCommit, Ref: "0123456789012345678901234567890123456789", ComposeFiles: []string{"compose.yaml"}}, true},
		{"embedded credential", Repository{CloneURL: "https://token@forge.example/deploy.git", RefType: RefBranch, Ref: "main", ComposeFiles: []string{"compose.yml"}}, false},
		{"local file", Repository{CloneURL: "file:///tmp/deploy", RefType: RefBranch, Ref: "main", ComposeFiles: []string{"compose.yml"}}, false},
		{"path escape", Repository{CloneURL: "https://forge.example/deploy.git", RefType: RefBranch, Ref: "main", ComposeFiles: []string{"../compose.yml"}}, false},
		{"environment escape", Repository{CloneURL: "https://forge.example/deploy.git", RefType: RefBranch, Ref: "main", ComposeFiles: []string{"compose.yml"}, EnvironmentFile: "../../.env"}, false},
		{"windows compose path", Repository{CloneURL: "https://forge.example/deploy.git", RefType: RefBranch, Ref: "main", ComposeFiles: []string{`production\compose.yml`}}, false},
		{"empty compose file", Repository{CloneURL: "https://forge.example/deploy.git", RefType: RefBranch, Ref: "main"}, false},
		{"invalid ref type", Repository{CloneURL: "https://forge.example/deploy.git", RefType: "revision", Ref: "main", ComposeFiles: []string{"compose.yml"}}, false},
		{"short commit", Repository{CloneURL: "https://forge.example/deploy.git", RefType: RefCommit, Ref: "0123456789abcdef", ComposeFiles: []string{"compose.yml"}}, false},
		{"ref traversal", Repository{CloneURL: "https://forge.example/deploy.git", RefType: RefBranch, Ref: "release/../main", ComposeFiles: []string{"compose.yml"}}, false},
		{"external helper", Repository{CloneURL: "ext::helper repository", RefType: RefBranch, Ref: "main", ComposeFiles: []string{"compose.yml"}}, false},
		{"git transport", Repository{CloneURL: "git://forge.example/team/deploy.git", RefType: RefBranch, Ref: "main", ComposeFiles: []string{"compose.yml"}}, false},
		{"query", Repository{CloneURL: "https://forge.example/team/deploy.git?token=value", RefType: RefBranch, Ref: "main", ComposeFiles: []string{"compose.yml"}}, false},
		{"ssh password", Repository{CloneURL: "ssh://git:password@forge.example/team/deploy.git", RefType: RefBranch, Ref: "main", ComposeFiles: []string{"compose.yml"}}, false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := ValidateRepository(test.repository)
			if (err == nil) != test.valid {
				t.Fatalf("valid = %v, error = %v", test.valid, err)
			}
		})
	}
}

func TestCanonicalRepositoryNormalizesTransportCaseAndSuffix(t *testing.T) {
	values := []string{
		"https://GIT.EXAMPLE.test/Team/Deploy.git",
		"git@git.example.test:Team/Deploy.git",
		"ssh://git@git.example.test/Team/Deploy.git",
	}
	for _, value := range values {
		got, err := CanonicalRepository(value)
		if err != nil {
			t.Fatalf("CanonicalRepository(%q): %v", value, err)
		}
		if got != "git.example.test/team/deploy" {
			t.Fatalf("CanonicalRepository(%q) = %q", value, got)
		}
	}
}

func TestCanonicalRepositoryRejectsUnsafeTransports(t *testing.T) {
	for _, value := range []string{
		"file:///tmp/deploy.git",
		"../deploy.git",
		"git://git.example.test/team/deploy.git",
		"https://user:secret@git.example.test/team/deploy.git",
		"https://git.example.test/team/../admin.git",
	} {
		t.Run(strings.ReplaceAll(value, "/", "_"), func(t *testing.T) {
			if got, err := CanonicalRepository(value); err == nil {
				t.Fatalf("CanonicalRepository(%q) unexpectedly returned %q", value, got)
			}
		})
	}
}
