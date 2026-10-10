package ai

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"unicode"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"
)

var errNoAvailableTarget = errors.New("没有可用的 AI 授权节点，请在设置 → AI 运维中授权并启用节点后重新提问。 / No enabled AI-authorized node is available. Authorize an enabled node in Settings → AI operations, then ask again.")

// Target resolution runs before the model receives any Docker tools. Only real
// directory entries and a server-validated context can establish a target.
type targetAgent struct{ frame *executionFrame }

func (*targetAgent) Name(context.Context) string { return "TargetResolver" }
func (*targetAgent) Description(context.Context) string {
	return "Resolve explicit task targets or request a node selection"
}
func (a *targetAgent) Run(ctx context.Context, _ *adk.AgentInput, _ ...adk.AgentRunOption) *adk.AsyncIterator[*adk.AgentEvent] {
	return a.resolve(ctx, nil)
}
func (a *targetAgent) Resume(ctx context.Context, in *adk.ResumeInfo, _ ...adk.AgentRunOption) *adk.AsyncIterator[*adk.AgentEvent] {
	return a.resolve(ctx, in)
}
func (a *targetAgent) resolve(ctx context.Context, resume *adk.ResumeInfo) *adk.AsyncIterator[*adk.AgentEvent] {
	iterator, writer := adk.NewAsyncIteratorPair[*adk.AgentEvent]()
	go func() {
		defer writer.Close()
		f := a.frame
		if err := f.guard(ctx); err != nil {
			writer.Send(&adk.AgentEvent{Err: err})
			return
		}
		if resume != nil && resume.WasInterrupted {
			state, ok := resume.InterruptState.(waitState)
			answer, answered := resume.ResumeData.(InputAnswer)
			if !ok || !resume.IsResumeTarget || !answered || state.InteractionID != answer.InteractionID {
				writer.Send(&adk.AgentEvent{Err: ErrConflict})
				return
			}
			if err := f.setTargets(answer.Values, "selection"); err != nil {
				writer.Send(&adk.AgentEvent{Err: err})
				return
			}
			writer.Send(adk.EventFromMessage(schema.AssistantMessage("Verified task targets: "+marshal(f.targets), nil), nil, schema.Assistant, ""))
			return
		}
		options, err := f.nodeOptions(ctx)
		if err != nil {
			writer.Send(&adk.AgentEvent{Err: err})
			return
		}
		text := strings.ToLower(f.row.Question)
		matches := []ResourceOption{}
		for _, option := range options {
			if mentionsTarget(text, option.ID) || option.Name != "" && mentionsTarget(text, option.Name) {
				matches = append(matches, option)
			}
		}
		all := strings.Contains(text, "所有节点") || strings.Contains(text, "全部节点") || strings.Contains(text, "all nodes") || strings.Contains(text, "every node")
		if all {
			ids := []string{}
			for _, option := range options {
				ids = append(ids, option.ID)
			}
			err = f.setTargets(ids, "explicit")
		} else if len(matches) == 1 {
			err = f.setTargets([]string{matches[0].ID}, "explicit")
		} else if len(matches) > 1 && explicitMultiple(text, matches) {
			ids := []string{}
			for _, match := range matches {
				ids = append(ids, match.ID)
			}
			err = f.setTargets(ids, "explicit")
		} else if len(matches) > 1 {
			options = matches
			f.targets = nil
			f.context = TargetContext{}
		} else if len(f.targets) > 0 {
			// A new topic must establish a new scope; pronouns/continue preserve it.
			reuse := f.source != "conversation" || refersToTask(text)
			if !reuse {
				messages := append([]*schema.Message{schema.SystemMessage(`Classify whether the current request continues the same task and verified node context as the previous turn. Return only JSON {"reuse_context":true} for a related follow-up, otherwise {"reuse_context":false}. Do not infer permission or new nodes.`)}, f.history(ctx)...)
				reply, e := (&ResponsesChatModel{frame: f}).Generate(ctx, messages)
				if e != nil {
					writer.Send(&adk.AgentEvent{Err: e})
					return
				}
				var choice struct {
					Reuse bool `json:"reuse_context"`
				}
				if json.Unmarshal([]byte(reply.Content), &choice) == nil {
					reuse = choice.Reuse
				}
			}
			if reuse {
				err = f.setTargets(f.targets, f.source)
			} else {
				f.targets = nil
				f.context = TargetContext{}
				f.source = ""
			}
		}
		if err != nil {
			writer.Send(&adk.AgentEvent{Err: err})
			return
		}
		if len(f.targets) > 0 {
			if f.context.ResourceID != "" && f.s.deps.Resources == nil {
				writer.Send(&adk.AgentEvent{Err: ErrInvalid})
				return
			}
			if f.context.ResourceID != "" && f.s.deps.Resources != nil {
				resources, e := f.s.deps.Resources(ctx, f.context.ResourceNodeID, ToolArgs{Kind: f.context.ResourceKind, ID: f.context.ResourceID})
				found := false
				for _, resource := range resources {
					if resource.ID == f.context.ResourceID {
						found = true
					}
				}
				if e != nil || !found {
					writer.Send(&adk.AgentEvent{Err: ErrConflict})
					return
				}
			}
			writer.Send(adk.EventFromMessage(schema.AssistantMessage("Verified task targets: "+marshal(f.targets), nil), nil, schema.Assistant, ""))
			return
		}
		// General explanations need no runtime. The classifier cannot authorize a
		// node; even a fabricated ID from a model still results in a human selection.
		general := false
		if len(matches) == 0 {
			reply, e := (&ResponsesChatModel{frame: f}).Generate(ctx, []*schema.Message{schema.SystemMessage(`Classify the request. Return only JSON {"general":true} for an explanation with no inspection or execution of real resources; otherwise {"general":false}. No tools or target inference.`), schema.UserMessage(f.row.Question)})
			if e != nil {
				writer.Send(&adk.AgentEvent{Err: e})
				return
			}
			var classification struct {
				General bool `json:"general"`
			}
			if json.Unmarshal([]byte(reply.Content), &classification) == nil {
				general = classification.General
			}
		}
		if general {
			writer.Send(adk.EventFromMessage(schema.AssistantMessage("General explanation: no Docker target is authorized for this task.", nil), nil, schema.Assistant, ""))
			return
		}
		if len(options) == 0 {
			writer.Send(&adk.AgentEvent{Err: errNoAvailableTarget})
			return
		}
		state := f.makeInput("node", "请确认本次任务的目标节点；若列表中没有所需节点，请先在设置 → AI 运维中授权。 / Confirm the target node. If it is missing, authorize it in Settings → AI operations.", options, all || len(matches) > 1, waitState{})
		writer.Send(adk.StatefulInterrupt(ctx, f.prompt(state), state))
	}()
	return iterator
}

func mentionsTarget(text, target string) bool {
	if target == "" {
		return false
	}
	target = strings.ToLower(target)
	for _, r := range target {
		if unicode.Is(unicode.Han, r) {
			return strings.Contains(text, target)
		}
	}
	return regexp.MustCompile(`(?:^|[^a-z0-9_-])` + regexp.QuoteMeta(target) + `(?:$|[^a-z0-9_-])`).MatchString(text)
}
func refersToTask(text string) bool {
	for _, value := range []string{"继续", "刚才", "它", "同一个", "continue", "same", "that one", "it ", "again", "next", "evidence"} {
		if strings.Contains(text, value) {
			return true
		}
	}
	return false
}
func explicitMultiple(text string, options []ResourceOption) bool {
	seen := map[string]bool{}
	for _, option := range options {
		if seen[strings.ToLower(option.Name)] {
			return false
		}
		seen[strings.ToLower(option.Name)] = true
	}
	return strings.Contains(text, " and ") || strings.Contains(text, "和") || strings.Contains(text, "与") || strings.Contains(text, "、") || strings.Contains(text, ",")
}
