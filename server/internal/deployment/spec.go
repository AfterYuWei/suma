package deployment

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/suma/suma/server/internal/compose"
	gitrepo "github.com/suma/suma/server/internal/git"
)

// Spec resolves only checked, regular files contained in the immutable worktree.
func Spec(name, worktree string, repository gitrepo.Repository) (compose.ExecutionSpec, error) {
	if err := gitrepo.ValidateRepository(repository); err != nil {
		return compose.ExecutionSpec{}, err
	}
	spec, err := ResolveSpec(name, worktree, repository.ComposeFiles, repository.EnvironmentFile)
	if err != nil {
		return spec, err
	}
	return spec, ValidateComposeSources(spec, worktree)
}

func ResolveSpec(name, worktree string, files []string, environmentFile string) (compose.ExecutionSpec, error) {
	if len(files) == 0 {
		return compose.ExecutionSpec{}, errors.New("release has no Compose files")
	}
	root, err := filepath.EvalSymlinks(worktree)
	if err != nil {
		return compose.ExecutionSpec{}, err
	}
	spec := compose.ExecutionSpec{ProjectName: name}
	resolve := func(value string) (string, error) {
		resolved, err := filepath.EvalSymlinks(filepath.Join(root, filepath.FromSlash(value)))
		if err != nil {
			return "", errors.New("deployment file is missing")
		}
		rel, err := filepath.Rel(root, resolved)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
			return "", errors.New("deployment file escapes Git worktree")
		}
		if _, err := ReadDeploymentFile(resolved); err != nil {
			return "", err
		}
		return resolved, nil
	}
	for _, file := range files {
		value, err := resolve(file)
		if err != nil {
			return spec, err
		}
		spec.Files = append(spec.Files, value)
	}
	spec.ProjectDir = filepath.Dir(spec.Files[0])
	if environmentFile != "" {
		value, err := resolve(environmentFile)
		if err != nil {
			return spec, err
		}
		spec.EnvFiles = []string{value}
	}
	return spec, nil
}

func ConfigurationHash(rendered string, environment map[string]string) string {
	if len(environment) > 0 {
		raw, _ := json.Marshal(environment)
		rendered += "\x00" + string(raw)
	}
	sum := sha256.Sum256([]byte(rendered))
	return hex.EncodeToString(sum[:])
}

func ValidateTarget(spec compose.ExecutionSpec, rendered string, remote bool) error {
	if err := ValidateDeploymentPolicy(rendered); err != nil {
		return err
	}
	for _, file := range spec.Files {
		raw, err := os.ReadFile(file)
		if err != nil {
			return err
		}
		if err := compose.ValidateComposeBindMounts(string(raw), remote, false); err != nil {
			return err
		}
	}
	return nil
}
