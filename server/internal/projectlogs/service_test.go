package projectlogs

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type fakeLogs struct {
	mu      sync.Mutex
	sources []Source
	records map[string][]Record
	active  atomic.Int32
	failed  string
}

func (f *fakeLogs) ProjectLogSources(context.Context, string) ([]Source, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Source{}, f.sources...), nil
}
func (f *fakeLogs) ReadProjectLogs(ctx context.Context, source Source, q Query, emit func(Record) error) error {
	f.active.Add(1)
	defer f.active.Add(-1)
	if source.ContainerID == f.failed {
		return errors.New("secret must never be reported")
	}
	if !q.Follow {
		for _, r := range f.records[source.ContainerID] {
			if err := emit(r); err != nil {
				return err
			}
		}
		return nil
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(5 * time.Millisecond):
	}
	record := Record{ID: source.ContainerID + "/live", ContainerID: source.ContainerID, ContainerName: source.ContainerName, Service: source.Service, Time: time.Now().UTC(), Stream: "stdout", Text: "live"}
	if err := emit(record); err != nil {
		return err
	}
	<-ctx.Done()
	return ctx.Err()
}
func logFixture() (*Service, *fakeLogs) {
	now := time.Now().UTC().Add(-time.Minute)
	f := &fakeLogs{sources: []Source{{ContainerID: "a", ContainerName: "web-1", Service: "web", State: "running"}, {ContainerID: "b", ContainerName: "api-1", Service: "api", State: "exited"}, {ContainerID: "one", Service: "web", OneOff: true}}, records: map[string][]Record{}}
	for i := 0; i < 20; i++ {
		id := "a"
		if i%2 == 1 {
			id = "b"
		}
		f.records[id] = append(f.records[id], Record{ID: fmt.Sprint(i), ContainerID: id, Time: now.Add(time.Duration(i) * time.Second), Stream: "stdout", Text: fmt.Sprint(i)})
	}
	return NewService(Dependencies{Runtime: func(context.Context, string) (Runtime, error) { return f, nil }}), f
}
func TestHistoryGlobalTailAndSelection(t *testing.T) {
	s, _ := logFixture()
	result, err := s.History(context.Background(), "node", "shop", Query{Tail: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Entries) != 5 || result.Entries[0].Text != "15" || !result.Truncated || len(result.Sources) != 2 {
		t.Fatalf("snapshot: %+v", result)
	}
	_, err = s.History(context.Background(), "node", "shop", Query{Tail: 5, Containers: []string{"another-project"}})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("cross project ID: %v", err)
	}
	result, err = s.History(context.Background(), "node", "shop", Query{Tail: 5, Services: []string{"api"}})
	if err != nil || len(result.Sources) != 1 || result.Sources[0].ContainerID != "b" {
		t.Fatalf("filter: %+v %v", result, err)
	}
}
func TestPartialFailureAndQueryValidation(t *testing.T) {
	s, f := logFixture()
	f.failed = "b"
	result, err := s.History(context.Background(), "node", "shop", Query{Tail: 200})
	if err != nil || len(result.Errors) != 1 || len(result.Entries) != 10 {
		t.Fatalf("partial: %+v %v", result, err)
	}
	if result.Errors[0].Message == "secret must never be reported" {
		t.Fatal("raw error escaped")
	}
	for _, q := range []Query{{Tail: 0}, {Tail: 5001}, {Tail: 200, Since: "bad"}, {Tail: 200, Since: "2026-10-02T02:00:00Z", Until: "2026-10-02T01:00:00Z"}, {Tail: 200, Follow: true, Until: "2026-10-02T01:00:00Z"}} {
		if q.Validate() == nil {
			t.Fatalf("accepted %+v", q)
		}
	}
	_, err = s.Sources(context.Background(), "node", "../escape")
	if !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
}
func TestFollowRebuildAndCancellation(t *testing.T) {
	s, f := logFixture()
	s.ReconcileInterval = 10 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	events := make(chan Event, 100)
	go func() {
		done <- s.Follow(ctx, "node", "shop", Query{Tail: 200}, func(event Event) error { events <- event; return nil })
	}()
	select {
	case event := <-events:
		if event.Type != "snapshot" {
			t.Fatal(event)
		}
	case <-time.After(time.Second):
		t.Fatal("no snapshot")
	}
	f.mu.Lock()
	f.sources = []Source{{ContainerID: "replacement", ContainerName: "web-2", Service: "web", State: "running"}}
	f.mu.Unlock()
	deadline := time.After(2 * time.Second)
	found := false
	for !found {
		select {
		case event := <-events:
			for _, row := range event.Entries {
				if row.ContainerID == "replacement" {
					found = true
				}
			}
		case <-deadline:
			t.Fatal("replacement logs not followed")
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("follow did not cancel")
	}
	if f.active.Load() != 0 {
		t.Fatalf("leaked streams: %d", f.active.Load())
	}
}
func TestBufferBytesAndSourceLimit(t *testing.T) {
	b := buffer{tail: 5000}
	for i := 0; i < 200; i++ {
		b.add(Record{ID: fmt.Sprint(i), Text: string(make([]byte, MaxLineBytes)), Time: time.Unix(int64(i), 0)})
	}
	if b.bytes > MaxBytes || !b.truncated {
		t.Fatalf("bytes %d", b.bytes)
	}
	sources := make([]Source, 65)
	_, err := SelectSources(sources, Query{})
	if !errors.Is(err, ErrTooManySources) {
		t.Fatal(err)
	}
}

type replayLogs struct {
	calls  atomic.Int32
	active atomic.Int32
	stamp  time.Time
}

func (f *replayLogs) ProjectLogSources(context.Context, string) ([]Source, error) {
	return []Source{{ContainerID: "a", Service: "web", State: "running"}}, nil
}
func (f *replayLogs) ReadProjectLogs(ctx context.Context, s Source, q Query, emit func(Record) error) error {
	f.active.Add(1)
	defer f.active.Add(-1)
	records := []Record{{ID: "old", ContainerID: "a", Time: f.stamp, Stream: "stdout", Text: "old"}}
	if q.Follow {
		n := f.calls.Add(1)
		records = append(records, Record{ID: "new/0", ContainerID: "a", Time: f.stamp.Add(time.Second), Stream: "stdout", Text: "same"}, Record{ID: "new/1", ContainerID: "a", Time: f.stamp.Add(time.Second), Stream: "stdout", Text: "same"})
		if n > 1 {
			records = append(records, Record{ID: "third", ContainerID: "a", Time: f.stamp.Add(2 * time.Second), Stream: "stdout", Text: "third"})
		}
	}
	for _, r := range records {
		if err := emit(r); err != nil {
			return err
		}
	}
	if q.Follow && f.calls.Load() == 1 {
		return errors.New("interrupted")
	}
	if q.Follow {
		<-ctx.Done()
		return ctx.Err()
	}
	return nil
}
func TestInterruptedSourceReplayMultiplicityAndShutdown(t *testing.T) {
	f := &replayLogs{stamp: time.Now().Add(-time.Minute)}
	s := NewService(Dependencies{Runtime: func(context.Context, string) (Runtime, error) { return f, nil }})
	s.ReconcileInterval = time.Millisecond * 10
	events := make(chan Event, 50)
	done := make(chan error, 1)
	go func() {
		done <- s.Follow(context.Background(), "node", "shop", Query{Tail: 200}, func(e Event) error { events <- e; return nil })
	}()
	same, third := 0, 0
	deadline := time.After(3 * time.Second)
	for third == 0 {
		select {
		case e := <-events:
			if e.Type == "entries" {
				for _, r := range e.Entries {
					if r.Text == "same" {
						same++
					}
					if r.Text == "third" {
						third++
					}
					if r.Text == "old" {
						t.Fatal("replayed snapshot boundary")
					}
				}
			}
		case <-deadline:
			t.Fatal("no resumed logs")
		}
	}
	if same != 2 || third != 1 {
		t.Fatalf("replay lost multiplicity or duplicated: %d %d", same, third)
	}
	s.Stop()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("shutdown did not release follow")
	}
	if f.active.Load() != 0 {
		t.Fatal("shutdown leaked Docker readers")
	}
}
