package projectlogs

import (
	"context"
	"crypto/sha256"
	"sync"
	"time"
)

type Service struct {
	deps              Dependencies
	lifetime          context.Context
	stop              context.CancelFunc
	mu                sync.Mutex
	wg                sync.WaitGroup
	closed            bool
	ReconcileInterval time.Duration
}

func replayKey(r Record) string {
	key := sha256.Sum256([]byte(r.Stream + "\x00" + r.Text))
	return string(key[:])
}
func NewService(deps Dependencies) *Service {
	ctx, cancel := context.WithCancel(context.Background())
	return &Service{deps: deps, lifetime: ctx, stop: cancel, ReconcileInterval: 5 * time.Second}
}
func (s *Service) Stop() { s.mu.Lock(); s.closed = true; s.stop(); s.mu.Unlock(); s.wg.Wait() }
func (s *Service) begin(ctx context.Context) (context.Context, func(), error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, nil, context.Canceled
	}
	s.wg.Add(1)
	child, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(s.lifetime, cancel)
	return child, func() { stop(); cancel(); s.wg.Done() }, nil
}
func (s *Service) Sources(ctx context.Context, node, name string) ([]Source, error) {
	if !projectName.MatchString(name) || len(name) > 128 {
		return nil, ErrInvalid
	}
	if s.deps.Exists != nil {
		if err := s.deps.Exists(ctx, node, name); err != nil {
			return nil, err
		}
	}
	runtime, err := s.deps.Runtime(ctx, node)
	if err != nil {
		return nil, err
	}
	sources, err := runtime.ProjectLogSources(ctx, name)
	if err != nil {
		return nil, err
	}
	if s.deps.Declared != nil {
		declared, err := s.deps.Declared(ctx, node, name)
		if err != nil {
			return nil, err
		}
		if declared != nil {
			for i := range sources {
				sources[i].Orphan = !sources[i].OneOff && !declared[sources[i].Service]
			}
		}
	}
	return sources, nil
}
func SelectSources(all []Source, q Query) ([]Source, error) {
	services := map[string]bool{}
	containers := map[string]bool{}
	found := map[string]bool{}
	for _, v := range q.Services {
		services[v] = true
	}
	for _, v := range q.Containers {
		containers[v] = true
	}
	selected := []Source{}
	for _, source := range all {
		if containers[source.ContainerID] {
			found[source.ContainerID] = true
		}
		if len(containers) > 0 && !containers[source.ContainerID] {
			continue
		}
		if len(services) > 0 && !services[source.Service] {
			continue
		}
		if source.OneOff && !q.IncludeOneOff && len(containers) == 0 {
			continue
		}
		selected = append(selected, source)
	}
	for id := range containers {
		if !found[id] {
			return nil, ErrInvalid
		}
	}
	if len(selected) > MaxSources {
		return nil, ErrTooManySources
	}
	return selected, nil
}
func (s *Service) History(ctx context.Context, node, name string, q Query) (Snapshot, error) {
	ctx, finish, err := s.begin(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	defer finish()
	q.Follow = false
	if err := q.Validate(); err != nil {
		return Snapshot{}, err
	}
	sources, err := s.Sources(ctx, node, name)
	if err != nil {
		return Snapshot{}, err
	}
	sources, err = SelectSources(sources, q)
	if err != nil {
		return Snapshot{}, err
	}
	runtime, err := s.deps.Runtime(ctx, node)
	if err != nil {
		return Snapshot{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	b := buffer{tail: q.Tail, entries: []Record{}}
	result := Snapshot{Sources: sources, Errors: []SourceError{}}
	var mu sync.Mutex
	var wg sync.WaitGroup
	slots := make(chan struct{}, 4)
	for _, source := range sources {
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case slots <- struct{}{}:
			case <-ctx.Done():
				mu.Lock()
				result.Errors = append(result.Errors, SourceError{source.ContainerID, "log query timed out"})
				mu.Unlock()
				return
			}
			defer func() { <-slots }()
			err := runtime.ReadProjectLogs(ctx, source, q, func(r Record) error { mu.Lock(); b.add(r); mu.Unlock(); return nil })
			if err != nil {
				mu.Lock()
				result.Errors = append(result.Errors, SourceError{source.ContainerID, "Unable to read retained container logs"})
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	result.Entries = b.entries
	result.Truncated = b.truncated || len(b.entries) >= q.Tail
	return result, nil
}

// Follow has one writer (emit), bounded input, and a cancellable reader per
// source. A failed source can never terminate another source's log stream.
func (s *Service) Follow(ctx context.Context, node, name string, q Query, emit func(Event) error) error {
	ctx, finish, err := s.begin(ctx)
	if err != nil {
		return err
	}
	defer finish()
	q.Follow = true
	if err := q.Validate(); err != nil {
		return err
	}
	runtimeKey := ""
	if s.deps.RuntimeKey != nil {
		runtimeKey, err = s.deps.RuntimeKey(ctx, node)
		if err != nil {
			return err
		}
	}
	snapshotQuery := q
	snapshotQuery.Follow = false
	startedAt := time.Now().UTC()
	snapshot, err := s.History(ctx, node, name, snapshotQuery)
	if err != nil {
		return err
	}
	if err := emit(Event{Type: "snapshot", Entries: snapshot.Entries, Sources: snapshot.Sources, Errors: snapshot.Errors, Truncated: snapshot.Truncated}); err != nil {
		return err
	}
	runtime, err := s.deps.Runtime(ctx, node)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	type message struct {
		r      *Record
		e      *SourceError
		source string
		done   bool
	}
	messages := make(chan message, 128)
	active := map[string]context.CancelFunc{}
	known := map[string]Source{}
	ended := map[string]bool{}
	var wg sync.WaitGroup
	defer func() {
		cancel()
		for _, stop := range active {
			stop()
		}
		wg.Wait()
	}()
	// Preserve multiplicity at equal timestamps; repeated text is legitimate.
	boundaries := map[string]time.Time{}
	replay := map[string]map[string]int{}
	skipReplay := map[string]map[string]int{}
	for _, r := range snapshot.Entries {
		if r.Time.After(boundaries[r.ContainerID]) {
			boundaries[r.ContainerID] = r.Time
			replay[r.ContainerID] = map[string]int{}
		}
		if r.Time.Equal(boundaries[r.ContainerID]) {
			replay[r.ContainerID][replayKey(r)]++
		}
	}
	start := func(source Source) {
		sourceCtx, stop := context.WithCancel(ctx)
		active[source.ContainerID] = stop
		known[source.ContainerID] = source
		skipReplay[source.ContainerID] = map[string]int{}
		for k, n := range replay[source.ContainerID] {
			skipReplay[source.ContainerID][k] = n
		}
		options := q
		if boundary := boundaries[source.ContainerID]; !boundary.IsZero() {
			options.Since = boundary.Format(time.RFC3339Nano)
		} else {
			options.Since = startedAt.Format(time.RFC3339Nano)
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := runtime.ReadProjectLogs(sourceCtx, source, options, func(r Record) error {
				select {
				case messages <- message{r: &r}:
					return nil
				case <-sourceCtx.Done():
					return sourceCtx.Err()
				}
			})
			var sourceErr *SourceError
			if err != nil && sourceCtx.Err() == nil {
				sourceErr = &SourceError{source.ContainerID, "Container log stream interrupted"}
			}
			select {
			case messages <- message{source: source.ContainerID, e: sourceErr, done: true}:
			case <-ctx.Done():
			}
		}()
	}
	for _, source := range snapshot.Sources {
		start(source)
	}
	refresh := time.NewTicker(s.ReconcileInterval)
	defer refresh.Stop()
	flush := time.NewTicker(200 * time.Millisecond)
	defer flush.Stop()
	batch := buffer{tail: q.Tail, entries: []Record{}}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case msg := <-messages:
			if msg.done {
				if stop := active[msg.source]; stop != nil {
					stop()
					delete(active, msg.source)
				}
				ended[msg.source] = true
				if msg.e != nil {
					if err := emit(Event{Type: "source_error", Errors: []SourceError{*msg.e}}); err != nil {
						return err
					}
					delete(ended, msg.source)
				}
			}
			if msg.r != nil {
				r := *msg.r
				boundary := boundaries[r.ContainerID]
				if r.Time.Before(boundary) {
					continue
				}
				if r.Time.Equal(boundary) {
					k := replayKey(r)
					if skipReplay[r.ContainerID][k] > 0 {
						skipReplay[r.ContainerID][k]--
						continue
					}
				}
				if r.Time.After(boundary) {
					boundaries[r.ContainerID] = r.Time
					replay[r.ContainerID] = map[string]int{}
					skipReplay[r.ContainerID] = map[string]int{}
				}
				if len(replay[r.ContainerID]) < 5000 {
					replay[r.ContainerID][replayKey(r)]++
				}
				batch.add(r)
			}
		case <-flush.C:
			if len(batch.entries) > 0 {
				if err := emit(Event{Type: "entries", Entries: batch.entries, Truncated: batch.truncated}); err != nil {
					return err
				}
				batch = buffer{tail: q.Tail, entries: []Record{}}
			}
		case <-refresh.C:
			if s.deps.RuntimeKey != nil {
				current, err := s.deps.RuntimeKey(ctx, node)
				if err != nil || current != runtimeKey {
					return context.Canceled
				}
			}
			all, err := s.Sources(ctx, node, name)
			if err != nil {
				if emit(Event{Type: "source_error", Errors: []SourceError{{Message: "Unable to refresh project instances"}}}) != nil {
					return err
				}
				continue
			}
			// Explicit instance selection may end after removal; never select another
			// project's container or silently substitute a replacement instance.
			if len(q.Containers) > 0 {
				existing := map[string]bool{}
				for _, v := range all {
					existing[v.ContainerID] = true
				}
				filtered := q
				filtered.Containers = nil
				for _, id := range q.Containers {
					if existing[id] {
						filtered.Containers = append(filtered.Containers, id)
					}
				}
				if len(filtered.Containers) == 0 {
					all = nil
				} else {
					all, err = SelectSources(all, filtered)
				}
			} else {
				all, err = SelectSources(all, q)
			}
			if err != nil {
				return err
			}
			changed := len(known) != len(all)
			for _, source := range all {
				if old, ok := known[source.ContainerID]; !ok || old != source {
					changed = true
				}
				if old, ok := known[source.ContainerID]; ok && old.State != source.State {
					delete(ended, source.ContainerID)
				}
			}
			present := map[string]bool{}
			for _, source := range all {
				present[source.ContainerID] = true
				if _, ok := active[source.ContainerID]; !ok && !ended[source.ContainerID] {
					start(source)
				}
			}
			for id, stop := range active {
				if !present[id] {
					stop()
					delete(active, id)
				}
			}
			if changed {
				known = map[string]Source{}
				for _, source := range all {
					known[source.ContainerID] = source
				}
				if err := emit(Event{Type: "sources", Sources: all}); err != nil {
					return err
				}
			}
		}
	}
}
