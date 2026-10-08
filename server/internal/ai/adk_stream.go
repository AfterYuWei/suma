package ai

import (
	"context"
	"time"
	"unicode"
	"unicode/utf8"
)

// Keep an unfinished word and a safety tail private. Redaction always sees the
// complete accumulated text, including open quoted secrets and private keys.
func stableModelText(f *executionFrame, raw string) string {
	text := f.clean(raw, 16000)
	n := len(text) - max(64, len(f.key)+1)
	if n <= 0 {
		return ""
	}
	for n > 0 {
		r, size := utf8.DecodeLastRuneInString(text[:n])
		if unicode.IsSpace(r) {
			return text[:n]
		}
		n -= size
	}
	return ""
}

func (m *ResponsesChatModel) streamModel(ctx context.Context, model StreamingModel, messages []ModelMessage, tools []Tool, emit func(string) error) (ModelReply, error) {
	requestCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	finished := make(chan struct{})
	guardError := make(chan error, 1)
	go func() {
		defer close(finished)
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-requestCtx.Done():
				return
			case <-ticker.C:
				if err := m.frame.guard(requestCtx); err != nil {
					guardError <- err
					cancel()
					return
				}
			}
		}
	}()
	reply, err := model.Stream(requestCtx, m.frame.cfg, m.frame.key, messages, tools, func(delta string) error {
		if err := requestCtx.Err(); err != nil {
			return err
		}
		return emit(delta)
	})
	cancel()
	<-finished
	select {
	case err := <-guardError:
		return ModelReply{}, err
	default:
		return reply, err
	}
}
