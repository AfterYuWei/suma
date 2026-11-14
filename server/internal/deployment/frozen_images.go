package deployment

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/distribution/reference"
	"github.com/opencontainers/go-digest"
	"github.com/suma/suma/server/internal/compose"
	"github.com/suma/suma/server/internal/credential"
	"github.com/suma/suma/server/internal/imageupdate"
)

type FrozenImage struct {
	Service      string
	Declared     string
	Reference    string
	Platform     string
	IndexDigest  string
	ConfigDigest string
}
type FrozenNode struct {
	RuntimeIdentity string
	ConfigHash      string
	Rendered        string
	Images          []FrozenImage
}
type FrozenInput struct {
	ForceRecreate        bool
	Nodes                map[string]FrozenNode
	ConfigurationVersion string
}

func FreezeNode(ctx context.Context, resolver imageupdate.Resolver, credentials *credential.RegistryService, ids []uint, target compose.Target, spec compose.ExecutionSpec, rendered string) (FrozenNode, error) {
	result := FrozenNode{RuntimeIdentity: target.RuntimeIdentity, ConfigHash: ConfigurationHash(rendered, spec.Environment), Rendered: rendered, Images: []FrozenImage{}}
	if resolver == nil || target.RuntimeIdentity == "" || target.OS == "" || target.Architecture == "" {
		return result, errors.New("release preparation requires a verified node platform and image resolver")
	}
	materials, err := releaseMaterials(ctx, credentials, ids, target.NodeID)
	if err != nil {
		return result, err
	}
	var document struct {
		Services map[string]struct {
			Image    string
			Platform string
		}
	}
	if err := json.Unmarshal([]byte(rendered), &document); err != nil {
		return result, err
	}
	services := make([]string, 0, len(document.Services))
	for name := range document.Services {
		services = append(services, name)
	}
	sort.Strings(services)
	for _, service := range services {
		declaration := document.Services[service]
		parsed, err := reference.ParseNormalizedNamed(declaration.Image)
		if err != nil {
			return result, fmt.Errorf("service %s has an invalid image reference", service)
		}
		platform := imageupdate.Platform{OS: target.OS, Architecture: target.Architecture, Variant: target.Variant}
		if declaration.Platform != "" {
			parts := strings.Split(declaration.Platform, "/")
			if len(parts) < 2 || len(parts) > 3 {
				return result, errors.New("invalid service platform")
			}
			platform.OS, platform.Architecture = parts[0], parts[1]
			platform.Variant = ""
			if len(parts) == 3 {
				platform.Variant = parts[2]
			}
		}
		remote, err := resolver.Resolve(ctx, reference.TagNameOnly(parsed).String(), platform, materials[imageupdate.RegistryHost(reference.Domain(parsed))])
		if err != nil {
			return result, fmt.Errorf("service %s image lookup failed: %w", service, err)
		}
		imageDigest, err := digest.Parse(remote.ManifestDigest)
		if err != nil {
			return result, errors.New("registry returned an invalid image digest")
		}
		pinned, err := reference.WithDigest(reference.TrimNamed(parsed), imageDigest)
		if err != nil {
			return result, errors.New("registry returned an invalid image digest")
		}
		result.Images = append(result.Images, FrozenImage{Service: service, Declared: declaration.Image, Reference: pinned.String(), Platform: strings.TrimSuffix(platform.String(), "/"), IndexDigest: remote.IndexDigest, ConfigDigest: remote.ConfigDigest})
	}
	if len(result.Images) == 0 {
		return result, errors.New("release has no deployable image services")
	}
	return result, nil
}

func releaseMaterials(ctx context.Context, credentials *credential.RegistryService, ids []uint, nodeID string) (map[string]credential.RegistryMaterial, error) {
	materials := map[string]credential.RegistryMaterial{}
	for _, id := range ids {
		if credentials == nil {
			return nil, errors.New("registry credentials unavailable")
		}
		if err := credentials.AuthorizedForNode(ctx, id, nodeID); err != nil {
			return nil, err
		}
		material, err := credentials.Material(ctx, id)
		if err != nil {
			return nil, err
		}
		host := imageupdate.RegistryHost(material.ServerAddress)
		if _, exists := materials[host]; exists {
			return nil, errors.New("multiple credentials for one registry")
		}
		materials[host] = material
	}

	return materials, nil
}

// Verification addresses the stored digest, never its old mutable tag.
func VerifyFrozenImages(ctx context.Context, resolver imageupdate.Resolver, credentials *credential.RegistryService, ids []uint, nodeID string, input FrozenNode) error {
	materials, err := releaseMaterials(ctx, credentials, ids, nodeID)
	if err != nil {
		return err
	}
	for _, image := range input.Images {
		parsed, err := reference.ParseNormalizedNamed(image.Reference)
		if err != nil {
			return errors.New("invalid frozen image identity")
		}
		pinned, ok := parsed.(reference.Digested)
		if !ok {
			return errors.New("frozen image is not digest-pinned")
		}
		parts := strings.Split(image.Platform, "/")
		if len(parts) < 2 || len(parts) > 3 {
			return errors.New("invalid frozen image platform")
		}
		platform := imageupdate.Platform{OS: parts[0], Architecture: parts[1]}
		if len(parts) == 3 {
			platform.Variant = parts[2]
		}
		remote, err := resolver.Resolve(ctx, image.Reference, platform, materials[imageupdate.RegistryHost(reference.Domain(parsed))])
		if err != nil {
			return fmt.Errorf("service %s pinned image is unavailable: %w", image.Service, err)
		}
		if remote.ManifestDigest != pinned.Digest().String() || remote.ConfigDigest != image.ConfigDigest {
			return errors.New("registry image no longer matches the recorded identity")
		}
	}
	return nil
}

func (input FrozenNode) Apply(spec compose.ExecutionSpec) compose.ExecutionSpec {
	spec.ImageOverrides = map[string]string{}
	spec.PlatformOverrides = map[string]string{}
	for _, image := range input.Images {
		spec.ImageOverrides[image.Service] = image.Reference
		spec.PlatformOverrides[image.Service] = image.Platform
	}
	return spec
}

func NodeChanges(target compose.Target, next FrozenNode, previous *FrozenNode, currentEnvironment, previousEnvironment map[string]string) (ReleaseNodeView, error) {
	view := ReleaseNodeView{NodeID: target.NodeID, NodeName: target.NodeName, Images: []ImageChange{}, Services: []ServiceChange{}, Environment: []VariableChange{}, Changed: previous == nil}
	secrets := map[string]string{}
	for name, value := range currentEnvironment {
		secrets["current_"+name] = value
	}
	for name, value := range previousEnvironment {
		secrets["previous_"+name] = value
	}
	var err error
	view.ComposeAfter, err = RedactConfiguration(next.Rendered, currentEnvironment)
	if err != nil {
		return view, err
	}
	if previous != nil {
		view.ComposeBefore, err = RedactConfiguration(previous.Rendered, previousEnvironment)
		if err != nil {
			return view, err
		}
	}
	oldImages := map[string]string{}
	if previous != nil {
		for _, image := range previous.Images {
			oldImages[image.Service] = image.Reference
		}
		view.Changed = previous.ConfigHash != next.ConfigHash
	}
	for _, image := range next.Images {
		change := ImageChange{Service: image.Service, Declared: compose.RedactEnvironment(image.Declared, secrets), Previous: compose.RedactEnvironment(oldImages[image.Service], secrets), Reference: compose.RedactEnvironment(image.Reference, secrets), Platform: image.Platform, Changed: oldImages[image.Service] != image.Reference}
		view.Images = append(view.Images, change)
		view.Changed = view.Changed || change.Changed
	}
	var after, before struct{ Services map[string]json.RawMessage }
	_ = json.Unmarshal([]byte(next.Rendered), &after)
	if previous != nil {
		_ = json.Unmarshal([]byte(previous.Rendered), &before)
	}
	names := map[string]bool{}
	for name := range after.Services {
		names[name] = true
	}
	for name := range before.Services {
		names[name] = true
	}
	for name := range names {
		action := "unchanged"
		if after.Services[name] == nil {
			action = "remove"
		} else if before.Services[name] == nil {
			action = "create"
		} else if !reflect.DeepEqual(after.Services[name], before.Services[name]) {
			action = "recreate"
		}
		for _, image := range view.Images {
			if image.Service == name && image.Changed && action == "unchanged" {
				action = "recreate"
			}
		}
		view.Services = append(view.Services, ServiceChange{Name: name, Action: action})
	}
	sort.Slice(view.Services, func(i, j int) bool { return view.Services[i].Name < view.Services[j].Name })
	variables := map[string]bool{}
	for name := range currentEnvironment {
		variables[name] = true
	}
	for name := range previousEnvironment {
		variables[name] = true
	}
	for name := range variables {
		value, exists := currentEnvironment[name]
		old, existed := previousEnvironment[name]
		action := "unchanged"
		if !exists {
			action = "remove"
		} else if !existed {
			action = "add"
		} else if value != old {
			action = "modify"
		}
		if action != "unchanged" {
			view.Environment = append(view.Environment, VariableChange{Name: name, Action: action})
		}
	}
	sort.Slice(view.Environment, func(i, j int) bool { return view.Environment[i].Name < view.Environment[j].Name })
	return view, nil
}
