package cd

import (
	"context"

	"github.com/suma/suma/server/internal/compose"
	"github.com/suma/suma/server/internal/database"
	"github.com/suma/suma/server/internal/deployment"
)

type EnvironmentVariable = deployment.EnvironmentVariable

func (s *Service) GetEnvironmentValues(ctx context.Context, name string) (map[string]string, error) {
	project, err := s.project(ctx, name)
	if err != nil {
		return nil, err
	}
	return s.decryptEnvironment(project.EnvironmentCiphertext)
}

func (s *Service) decryptEnvironment(value []byte) (map[string]string, error) {
	return deployment.DecryptEnvironment(s.secrets, value)
}
func (s *Service) configureEnvironment(value []byte, input []EnvironmentVariable) ([]byte, error) {
	return deployment.ConfigureEnvironment(s.secrets, value, input)
}
func (s *Service) environmentSummary(value []byte) ([]EnvironmentVariable, error) {
	return deployment.EnvironmentSummary(s.secrets, value)
}
func (s *Service) releaseSpec(projectName string, release database.DeliveryRelease) (compose.ExecutionSpec, error) {
	spec, err := releaseExecutionSpec(projectName, release)
	if err != nil {
		return spec, err
	}
	spec.Environment, err = s.decryptEnvironment(release.EnvironmentCiphertext)
	return spec, err
}

// Include even unused manual values so changing them creates an independently
// reviewable candidate, while retaining the hash of pre-upgrade releases.
var releaseConfigurationHash = deployment.ConfigurationHash
