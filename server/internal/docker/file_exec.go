package docker

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	dockercontainer "github.com/docker/docker/api/types/container"
	"github.com/docker/docker/pkg/stdcopy"
)

type cappedBuffer struct {
	bytes.Buffer
	max int
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	if b.Len()+len(p) > b.max {
		return 0, errors.New("container command output exceeds limit")
	}
	return b.Buffer.Write(p)
}

// RunFileCommand executes only server-owned scripts, passing paths as positional
// arguments. It uses the container's configured user and never invokes host shell.
func (a *Adapter) RunFileCommand(ctx context.Context, id, script string, args []string, input []byte, maxOutput int) ([]byte, error) {
	if maxOutput < 1 {
		maxOutput = 64 << 10
	}
	cmd := append([]string{"sh", "-c", script, "--"}, args...)
	created, err := a.client.ContainerExecCreate(ctx, id, dockercontainer.ExecOptions{
		AttachStdin: input != nil, AttachStdout: true, AttachStderr: true, Cmd: cmd,
	})
	if err != nil {
		return nil, fmt.Errorf("container file tools unavailable: %w", err)
	}
	response, err := a.client.ContainerExecAttach(ctx, created.ID, dockercontainer.ExecAttachOptions{})
	if err != nil {
		return nil, fmt.Errorf("start container file command: %w", err)
	}
	defer response.Close()
	closed := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			response.Close()
		case <-closed:
		}
	}()
	defer close(closed)
	if input != nil {
		go func() {
			_, _ = io.Copy(response.Conn, bytes.NewReader(input))
			_ = response.CloseWrite()
		}()
	}
	out := &cappedBuffer{max: maxOutput}
	stderr := &cappedBuffer{max: 16 << 10}
	_, readErr := stdcopy.StdCopy(out, stderr, response.Reader)
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if readErr != nil {
		return nil, readErr
	}
	result, err := a.client.ContainerExecInspect(ctx, created.ID)
	if err != nil {
		return nil, err
	}
	if result.ExitCode != 0 {
		message := strings.TrimSpace(stderr.String())
		if message == "" {
			message = "container file command failed"
		}
		return nil, fmt.Errorf("%s (exit %d)", message, result.ExitCode)
	}
	return out.Bytes(), nil
}
