package docker

import (
	"context"
	"fmt"
	dockercontainer "github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/pkg/stdcopy"
	"github.com/suma/suma/server/internal/projectlogs"
	"io"
	"strconv"
	"strings"
	"time"
)

func (a *Adapter) ProjectLogSources(ctx context.Context, name string) ([]projectlogs.Source, error) {
	rows, err := a.client.ContainerList(ctx, dockercontainer.ListOptions{All: true, Filters: filters.NewArgs(filters.Arg("label", "com.docker.compose.project="+name))})
	if err != nil {
		return nil, err
	}
	result := []projectlogs.Source{}
	for _, row := range rows {
		// Do not trust a daemon's filter response as the authorization boundary.
		if row.Labels["com.docker.compose.project"] != name {
			continue
		}
		instance, _ := strconv.Atoi(row.Labels["com.docker.compose.container-number"])
		result = append(result, projectlogs.Source{ContainerID: row.ID, ContainerName: shortContainerName(row.ID, row.Names), Service: row.Labels["com.docker.compose.service"], Instance: instance, State: string(row.State), OneOff: strings.EqualFold(row.Labels["com.docker.compose.oneoff"], "true")})
	}
	return result, nil
}
func (a *Adapter) ReadProjectLogs(ctx context.Context, source projectlogs.Source, q projectlogs.Query, emit func(projectlogs.Record) error) error {
	inspect, err := a.client.ContainerInspect(ctx, source.ContainerID)
	if err != nil {
		return err
	}
	response, err := a.client.ContainerLogs(ctx, source.ContainerID, dockercontainer.LogsOptions{ShowStdout: true, ShowStderr: true, Timestamps: true, Since: q.Since, Until: q.Until, Tail: strconv.Itoa(q.Tail), Follow: q.Follow})
	if err != nil {
		return err
	}
	defer response.Close()
	stop := context.AfterFunc(ctx, func() { _ = response.Close() })
	defer stop()
	makeWriter := func(stream string) *projectLineWriter {
		return &projectLineWriter{source: source, stream: stream, emit: emit}
	}
	if inspect.Config != nil && inspect.Config.Tty {
		writer := makeWriter("tty")
		_, err = io.Copy(writer, response)
		if err == nil && len(writer.pending) > 0 {
			err = writer.flush()
		}
		return err
	}
	stdout, stderr := makeWriter("stdout"), makeWriter("stderr")
	_, err = stdcopy.StdCopy(stdout, stderr, response)
	if err == nil && len(stdout.pending) > 0 {
		err = stdout.flush()
	}
	if err == nil && len(stderr.pending) > 0 {
		err = stderr.flush()
	}
	return err
}

type projectLineWriter struct {
	source    projectlogs.Source
	stream    string
	emit      func(projectlogs.Record) error
	pending   []byte
	truncated bool
	lastTime  string
	ordinal   uint64
}

func (w *projectLineWriter) Write(value []byte) (int, error) {
	size := len(value)
	for _, b := range value {
		if b == '\n' {
			if err := w.flush(); err != nil {
				return 0, err
			}
			continue
		}
		if len(w.pending) < projectlogs.MaxLineBytes {
			w.pending = append(w.pending, b)
		} else {
			w.truncated = true
		}
	}
	return size, nil
}
func (w *projectLineWriter) flush() error {
	raw := strings.TrimSuffix(string(w.pending), "\r")
	w.pending = nil
	stamp, text, has := strings.Cut(raw, " ")
	t, err := time.Parse(time.RFC3339Nano, stamp)
	if !has || err != nil {
		t = time.Now().UTC()
		text = raw
	}
	if w.lastTime == stamp {
		w.ordinal++
	} else {
		w.lastTime = stamp
		w.ordinal = 0
	}
	r := projectlogs.Record{ID: fmt.Sprintf("%s/%s/%s/%d", w.source.ContainerID, stamp, w.stream, w.ordinal), Time: t, Service: w.source.Service, ContainerID: w.source.ContainerID, ContainerName: w.source.ContainerName, Stream: w.stream, Text: text, Truncated: w.truncated}
	w.truncated = false
	return w.emit(r)
}
