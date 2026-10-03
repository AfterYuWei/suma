package registry

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/suma/suma/server/internal/credential"
	"github.com/suma/suma/server/internal/imageupdate"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func digest(body []byte) string {
	d := sha256.Sum256(body)
	return "sha256:" + hex.EncodeToString(d[:])
}
func TestHTTPSMultiPlatformMetadataOnly(t *testing.T) {
	manifests := map[string][]byte{}
	configs := map[string]string{}
	configBodies := map[string][]byte{}
	children := []map[string]any{}
	for _, arch := range []string{"amd64", "arm64"} {
		configBody, _ := json.Marshal(map[string]string{"os": "linux", "architecture": arch})
		configID := digest(configBody)
		configBodies[configID] = configBody
		configs[arch] = configID
		body, _ := json.Marshal(map[string]any{"schemaVersion": 2, "mediaType": "application/vnd.oci.image.manifest.v1+json", "config": map[string]any{"mediaType": "application/vnd.oci.image.config.v1+json", "digest": configID, "size": len(configBody)}, "layers": []any{}})
		d := digest(body)
		manifests[d] = body
		children = append(children, map[string]any{"mediaType": "application/vnd.oci.image.manifest.v1+json", "digest": d, "size": len(body), "platform": map[string]string{"os": "linux", "architecture": arch, "variant": map[string]string{"arm64": "v8"}[arch]}})
	}
	index, _ := json.Marshal(map[string]any{"schemaVersion": 2, "mediaType": "application/vnd.oci.image.index.v1+json", "manifests": children})
	manifests["latest"] = index
	var blobs atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v2/" {
			return
		}
		if strings.Contains(r.URL.Path, "/blobs/") {
			if body := configBodies[strings.TrimPrefix(r.URL.Path, "/v2/team/app/blobs/")]; body != nil {
				_, _ = w.Write(body)
				return
			}
			blobs.Add(1)
			http.NotFound(w, r)
			return
		}
		k := strings.TrimPrefix(r.URL.Path, "/v2/team/app/manifests/")
		body := manifests[k]
		if body == nil {
			http.NotFound(w, r)
			return
		}
		media := "application/vnd.oci.image.manifest.v1+json"
		if k == "latest" {
			media = "application/vnd.oci.image.index.v1+json"
		}
		w.Header().Set("Content-Type", media)
		w.Header().Set("Docker-Content-Digest", digest(body))
		_, _ = w.Write(body)
	}))
	defer server.Close()
	adapter := Adapter{Transport: server.Client().Transport}
	ref := strings.TrimPrefix(server.URL, "https://") + "/team/app:latest"
	for _, arch := range []string{"amd64", "arm64"} {
		result, err := adapter.Resolve(context.Background(), ref, imageupdate.Platform{OS: "linux", Architecture: arch, Variant: map[string]string{"arm64": "v8"}[arch]}, credential.RegistryMaterial{})
		if err != nil {
			t.Fatal(err)
		}
		if result.ConfigDigest != configs[arch] {
			t.Fatalf("wrong platform: %+v", result)
		}
	}
	_, err := adapter.Resolve(context.Background(), ref, imageupdate.Platform{OS: "linux", Architecture: "arm64", Variant: "v7"}, credential.RegistryMaterial{})
	if err == nil || err.Error() != "platform_unavailable" {
		t.Fatalf("missing variant: %v", err)
	}
	// Updating only another architecture must not change the selected config.
	children[1]["digest"] = "sha256:" + strings.Repeat("ef", 32)
	updatedIndex, _ := json.Marshal(map[string]any{"schemaVersion": 2, "mediaType": "application/vnd.oci.image.index.v1+json", "manifests": children})
	manifests["latest"] = updatedIndex
	same, err := adapter.Resolve(context.Background(), ref, imageupdate.Platform{OS: "linux", Architecture: "amd64"}, credential.RegistryMaterial{})
	if err != nil || same.ConfigDigest != configs["amd64"] {
		t.Fatalf("other architecture caused false update: %+v %v", same, err)
	}
	if blobs.Load() != 0 {
		t.Fatal("downloaded an image blob")
	}
	_, err = (Adapter{}).Resolve(context.Background(), ref, imageupdate.Platform{OS: "linux", Architecture: "amd64"}, credential.RegistryMaterial{})
	if err == nil {
		t.Fatal("accepted untrusted certificate")
	}
}
func TestBasicAuthenticationAndErrorSanitization(t *testing.T) {
	secret := "private-registry-password"
	configBody := []byte(`{"os":"linux","architecture":"amd64"}`)
	configID := digest(configBody)
	body := fmt.Sprintf(`{"schemaVersion":2,"mediaType":"application/vnd.oci.image.manifest.v1+json","config":{"mediaType":"application/vnd.oci.image.config.v1+json","size":%d,"digest":%q},"layers":[]}`, len(configBody), configID)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, password, ok := r.BasicAuth()
		if !ok || user != "user" || password != secret {
			w.Header().Set("WWW-Authenticate", `Basic realm="registry"`)
			w.WriteHeader(401)
			_, _ = w.Write([]byte(secret))
			return
		}
		if r.URL.Path == "/v2/" {
			return
		}
		if strings.Contains(r.URL.Path, "/blobs/") {
			_, _ = w.Write(configBody)
			return
		}
		w.Header().Set("Content-Type", "application/vnd.oci.image.manifest.v1+json")
		_, _ = w.Write([]byte(body))
	}))
	defer server.Close()
	a := Adapter{Transport: server.Client().Transport}
	ref := strings.TrimPrefix(server.URL, "https://") + "/team/app:latest"
	result, err := a.Resolve(context.Background(), ref, imageupdate.Platform{OS: "linux", Architecture: "amd64"}, credential.RegistryMaterial{AuthType: "basic", Username: "user", Secret: secret})
	if err != nil || result.ConfigDigest != configID {
		t.Fatalf("auth: %+v %v", result, err)
	}
	_, err = a.Resolve(context.Background(), ref, imageupdate.Platform{OS: "linux", Architecture: "amd64"}, credential.RegistryMaterial{})
	if err == nil || strings.Contains(err.Error(), secret) {
		t.Fatalf("unsafe error: %v", err)
	}
}

func TestRegistryRateLimitAndTimeout(t *testing.T) {
	for _, code := range []int{429, 504} {
		t.Run(fmt.Sprint(code), func(t *testing.T) {
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if code == 504 {
					<-r.Context().Done()
					return
				}
				w.WriteHeader(code)
			}))
			defer server.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
			defer cancel()
			_, err := (Adapter{Transport: server.Client().Transport}).Resolve(ctx, strings.TrimPrefix(server.URL, "https://")+"/team/app:latest", imageupdate.Platform{OS: "linux", Architecture: "amd64"}, credential.RegistryMaterial{})
			expected := "rate_limited"
			if code == 504 {
				expected = "timeout"
			}
			if err == nil || err.Error() != expected {
				t.Fatalf("reason: %v", err)
			}
		})
	}
}
