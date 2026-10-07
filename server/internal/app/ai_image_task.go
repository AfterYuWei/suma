package app

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/go-containerregistry/pkg/name"
	"github.com/suma/suma/server/internal/ai"
	"github.com/suma/suma/server/internal/audit"
	"github.com/suma/suma/server/internal/database"
	"github.com/suma/suma/server/internal/imageupdate"
	"github.com/suma/suma/server/internal/registry"
	"github.com/suma/suma/server/internal/task"
)

func (r aiRuntime) Check(ctx context.Context, nodeID string, args ai.ToolArgs, actor ai.Actor) (database.Task, error) {
	if args.Query == "" {
		return r.imageUpdates.Check(ctx, nodeID, imageupdate.CheckInput{ImageIDs: []string{args.ID}}, imageupdate.Actor{UserID: &actor.UserID, IP: actor.IP})
	}
	parsed, err := name.ParseReference(args.Query, name.StrictValidation)
	if err != nil {
		return database.Task{}, ai.ErrInvalid
	}
	node, err := r.nodes.Get(ctx, nodeID)
	if err != nil || !node.Enabled {
		return database.Task{}, ai.ErrScope
	}
	if err = audit.NewService(r.db).RecordAI(ctx, r.db, database.AIAudit{UserID: actor.UserID, Source: actor.Source, BindingID: actor.BindingID, ExternalUserID: actor.ExternalUserID, ChatID: actor.ChatID, NodeID: nodeID, Action: "image.resolve", Resource: parsed.Name(), Result: "requested"}); err != nil {
		return database.Task{}, err
	}
	return r.tasks.StartWithIDForNode(nodeID, node.Name, "image.check", "Resolve image digest", func(ctx context.Context, _ string, report task.Reporter) error {
		ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		runtime, err := r.nodes.Runtime(ctx, nodeID)
		if err != nil {
			return err
		}
		info, err := runtime.Info(ctx)
		if err != nil {
			return err
		}
		_, material, err := r.pullConfiguration(ctx, nodeID, parsed.Name(), true)
		if err != nil {
			return err
		}
		platform := imageupdate.Platform{OS: info.OSType, Architecture: info.Architecture}
		switch platform.Architecture {
		case "x86_64":
			platform.Architecture = "amd64"
		case "aarch64":
			platform.Architecture = "arm64"
		case "armv7l":
			platform.Architecture = "arm"
			platform.Variant = "v7"
		}
		remote, err := (registry.Adapter{}).Resolve(ctx, parsed.Name(), platform, material)
		if err != nil {
			return errors.New("registry digest resolution failed; inspect image check task")
		}
		if !strings.HasPrefix(remote.ManifestDigest, "sha256:") || len(remote.ManifestDigest) != 71 {
			return ai.ErrInvalid
		}
		fixed := parsed.Context().Name() + "@" + remote.ManifestDigest
		report(95, "Pinned image reference: "+fixed)
		return nil
	})
}
