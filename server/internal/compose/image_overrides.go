package compose

import (
	"encoding/json"
	"errors"
	"os"
)

// Only ComposeRunner writes this private overlay; Git files stay immutable.
func prepareImageOverrides(spec ExecutionSpec) (ExecutionSpec, func(), error) {
	noop := func() {}
	if len(spec.ImageOverrides) == 0 {
		return spec, noop, nil
	}
	services := map[string]map[string]string{}
	for name, image := range spec.ImageOverrides {
		if name == "" || image == "" {
			return spec, noop, errors.New("invalid frozen service image")
		}
		services[name] = map[string]string{"image": image}
		if platform := spec.PlatformOverrides[name]; platform != "" {
			services[name]["platform"] = platform
		}
	}
	raw, err := json.Marshal(map[string]any{"services": services})
	if err != nil {
		return spec, noop, err
	}
	file, err := os.CreateTemp("", "suma-release-images-*.json")
	if err != nil {
		return spec, noop, errors.New("cannot create private image overlay")
	}
	cleanup := func() { _ = os.Remove(file.Name()) }
	_, writeErr := file.Write(raw)
	closeErr := file.Close()
	if writeErr != nil || closeErr != nil {
		cleanup()
		return spec, noop, errors.New("cannot write private image overlay")
	}
	spec.Files = append(append([]string(nil), spec.Files...), file.Name())
	return spec, cleanup, nil
}
