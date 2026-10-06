package ai

import (
	"context"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/suma/suma/server/internal/outbound"
)

func validateEndpoint(endpoint string, allowHTTP bool) error {
	if len(endpoint) > 2048 || outbound.Validate(endpoint, allowHTTP) != nil {
		return fmt.Errorf("%w: use an HTTPS API base URL, or explicitly allow insecure HTTP", ErrInvalid)
	}
	u, err := url.Parse(endpoint)
	if err != nil || u.RawQuery != "" || u.ForceQuery {
		return fmt.Errorf("%w: API base URLs cannot contain a query", ErrInvalid)
	}
	for _, suffix := range []string{"/chat/completions", "/responses", "/models"} {
		if strings.HasSuffix(strings.TrimRight(u.Path, "/"), suffix) {
			return fmt.Errorf("%w: enter the API base URL without /chat/completions, /responses or /models", ErrInvalid)
		}
	}
	return nil
}

func validModel(id string) bool {
	return id != "" && len(id) <= 256 && !strings.ContainsAny(id, ",，") && !strings.ContainsFunc(id, unicode.IsControl)
}

func normalizeModels(models []string, current string) ([]string, error) {
	// Older clients supply only the single default model.
	if len(models) == 0 && strings.TrimSpace(current) != "" {
		models = []string{current}
	}
	if len(models) > 200 {
		return nil, fmt.Errorf("%w: select at most 200 models", ErrInvalid)
	}
	result := []string{}
	for _, model := range models {
		model = strings.TrimSpace(model)
		if !validModel(model) {
			return nil, fmt.Errorf("%w: model IDs must contain 1–256 characters without commas or control characters", ErrInvalid)
		}
		if !has(result, model) {
			result = append(result, model)
		}
	}
	return result, nil
}

func (s *Service) DiscoverModels(ctx context.Context, in ModelsInput) ([]string, error) {
	cfg := Settings{Endpoint: strings.TrimRight(strings.TrimSpace(in.Endpoint), "/"), AllowPrivate: in.AllowPrivate, AllowInsecure: in.AllowInsecure}
	if err := validateEndpoint(cfg.Endpoint, cfg.AllowInsecure); err != nil {
		return nil, err
	}
	if len(in.APIKey) > 8192 || strings.ContainsAny(in.APIKey, "\r\n") {
		return nil, ErrInvalid
	}
	s.mu.Lock()
	if s.stopped || in.Version != s.cfg.Version {
		s.mu.Unlock()
		return nil, ErrConflict
	}
	key := s.key
	s.mu.Unlock()
	if in.APIKey != "" {
		key = in.APIKey
	}
	lister, ok := s.deps.Model.(interface {
		ListModels(context.Context, Settings, string) ([]string, error)
	})
	if !ok {
		return nil, fmt.Errorf("%w: this model adapter does not support discovery", ErrInvalid)
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	models, err := lister.ListModels(ctx, cfg, key)
	if err != nil {
		return nil, err
	}
	sort.Strings(models)
	return models, nil
}
