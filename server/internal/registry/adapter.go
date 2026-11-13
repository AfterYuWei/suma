// Package registry implements authenticated HTTPS metadata access without
// loading a host keychain or downloading image layers.
package registry

import (
	"context"
	"errors"
	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/remote/transport"
	"github.com/google/go-containerregistry/pkg/v1/types"
	"github.com/suma/suma/server/internal/credential"
	"github.com/suma/suma/server/internal/imageupdate"
	"net/http"
	"strings"
)

type Adapter struct{ Transport http.RoundTripper }
type httpsTransport struct {
	base http.RoundTripper
	host string
}

func (t httpsTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.Scheme != "https" {
		if r.URL.Scheme != "http" || r.URL.Host != t.host {
			return nil, errors.New("registry requires HTTPS")
		}
		cloned := r.Clone(r.Context())
		u := *r.URL
		u.Scheme = "https"
		cloned.URL = &u
		r = cloned
	}
	return t.base.RoundTrip(r)
}
func (a Adapter) Resolve(ctx context.Context, value string, p imageupdate.Platform, m credential.RegistryMaterial) (imageupdate.Remote, error) {
	ref, err := name.ParseReference(value, name.StrictValidation)
	if err != nil {
		return imageupdate.Remote{}, &imageupdate.LookupError{Code: "invalid_reference"}
	}
	config := authn.AuthConfig{}
	if m.AuthType == credential.RegistryToken {
		config.IdentityToken = m.Secret
	} else {
		config.Username = m.Username
		config.Password = m.Secret
	}
	base := a.Transport
	if base == nil {
		base = remote.DefaultTransport
	}
	descriptor, err := remote.Get(ref, remote.WithContext(ctx), remote.WithAuth(authn.FromConfig(config)), remote.WithPlatform(v1.Platform{OS: p.OS, Architecture: p.Architecture, Variant: p.Variant}), remote.WithTransport(httpsTransport{base: base, host: ref.Context().RegistryStr()}), remote.WithUserAgent("SUMA image-update"))
	if err != nil {
		return imageupdate.Remote{}, lookupError(ctx, err)
	}
	img, err := descriptor.Image()
	if err != nil {
		return imageupdate.Remote{}, lookupError(ctx, err)
	}
	manifest, err := img.Manifest()
	if err != nil {
		return imageupdate.Remote{}, lookupError(ctx, err)
	}
	digest, err := img.Digest()
	if err != nil {
		return imageupdate.Remote{}, lookupError(ctx, err)
	}
	imageConfig, err := img.ConfigFile()
	if err != nil {
		return imageupdate.Remote{}, lookupError(ctx, err)
	}
	if imageConfig.OS != p.OS || imageConfig.Architecture != p.Architecture || (p.Variant != "" && imageConfig.Variant != p.Variant && (imageConfig.Variant != "" || (descriptor.MediaType != types.OCIImageIndex && descriptor.MediaType != types.DockerManifestList))) {
		return imageupdate.Remote{}, &imageupdate.LookupError{Code: "platform_unavailable"}
	}
	return imageupdate.Remote{ManifestDigest: digest.String(), ConfigDigest: manifest.Config.Digest.String()}, nil
}
func lookupError(ctx context.Context, err error) error {
	code := "registry_unreachable"
	var e *transport.Error
	if errors.As(err, &e) {
		switch e.StatusCode {
		case 401, 403:
			code = "authentication_required"
		case 404:
			code = "reference_not_found"
		case 429:
			code = "rate_limited"
		}
	}
	if ctx.Err() != nil {
		code = "timeout"
	}
	if strings.Contains(err.Error(), "no child with platform") {
		code = "platform_unavailable"
	}
	if strings.Contains(err.Error(), "unsupported MediaType") {
		code = "unsupported_manifest"
	}
	return &imageupdate.LookupError{Code: code}
}
