package ai

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
)

// A successful EOF is insufficient: Responses must report completion before
// tool calls can enter Eino. Only output_text deltas are visible while reading.
func readResponsesStream(ctx context.Context, body io.Reader, emit func(string) error) (ModelReply, error) {
	reader := &io.LimitedReader{R: body, N: (1 << 20) + 1}
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 4096), 1<<20)
	var data []string
	var frameSize int
	process := func() (ModelReply, bool, error) {
		if len(data) == 0 {
			return ModelReply{}, false, nil
		}
		payload := strings.Join(data, "\n")
		data, frameSize = nil, 0
		if payload == "[DONE]" {
			return ModelReply{}, false, nil
		}
		var event struct {
			Type     string          `json:"type"`
			Delta    string          `json:"delta"`
			Response json.RawMessage `json:"response"`
		}
		if json.Unmarshal([]byte(payload), &event) != nil {
			return ModelReply{}, false, errors.New("invalid model streaming event")
		}
		switch event.Type {
		case "response.output_text.delta":
			if event.Delta != "" {
				if err := emit(event.Delta); err != nil {
					return ModelReply{}, false, err
				}
			}
		case "response.completed":
			var completed struct {
				Status string `json:"status"`
			}
			if json.Unmarshal(event.Response, &completed) != nil || completed.Status != "completed" {
				return ModelReply{}, false, errors.New("model stream did not complete")
			}
			reply, err := parseModelReply(event.Response)
			return reply, true, err
		case "response.failed", "response.incomplete", "error":
			return ModelReply{}, false, errors.New("model stream failed before completion")
		}
		return ModelReply{}, false, nil
	}
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return ModelReply{}, err
		}
		line := strings.TrimSuffix(scanner.Text(), "\r")
		if reader.N <= 0 {
			return ModelReply{}, errors.New("model response exceeded the size limit")
		}
		if line == "" {
			reply, completed, err := process()
			if completed || err != nil {
				return reply, err
			}
		} else if strings.HasPrefix(line, "data:") {
			value := strings.TrimPrefix(line, "data:")
			value = strings.TrimPrefix(value, " ")
			frameSize += len(value)
			if frameSize > 1<<20 {
				return ModelReply{}, errors.New("model response exceeded the size limit")
			}
			data = append(data, value)
		}
	}
	if err := ctx.Err(); err != nil {
		return ModelReply{}, err
	}
	if scanner.Err() != nil || reader.N <= 0 {
		return ModelReply{}, errors.New("model stream ended unexpectedly or exceeded the size limit")
	}
	if reply, completed, err := process(); completed || err != nil {
		return reply, err
	}
	return ModelReply{}, errors.New("model stream ended before completion")
}
