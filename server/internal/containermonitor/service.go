package containermonitor

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	domain "github.com/suma/suma/server/internal/container"
	"github.com/suma/suma/server/internal/database"
	"github.com/suma/suma/server/internal/task"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type latest struct {
	sample       domain.Metrics
	received     time.Time
	previous     *domain.Metrics
	lastRecorded time.Time
	session      string
	owner        string
	nodeID       string
}
type worker struct {
	key    string
	cancel context.CancelFunc
	done   chan struct{}
}
type Service struct {
	db            *gorm.DB
	tasks         *task.Service
	deps          Dependencies
	mu            sync.RWMutex
	policy        Policy
	activeSince   time.Time
	live          map[string]latest
	statuses      map[string]NodeStatus
	workers       map[string]worker
	cancel        context.CancelFunc
	wg            sync.WaitGroup
	maintenanceMu sync.Mutex
	pruneLock     sync.Mutex
}

func New(db *gorm.DB, tasks *task.Service, deps Dependencies) (*Service, error) {
	s := &Service{db: db, tasks: tasks, deps: deps, live: map[string]latest{}, statuses: map[string]NodeStatus{}, workers: map[string]worker{}}
	var row database.MonitoringPolicy
	if err := db.First(&row, 1).Error; err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(row.PolicyJSON), &s.policy); err != nil {
		return nil, err
	}
	s.policy.Revision = row.Revision
	s.activeSince = row.UpdatedAt
	if err := validatePolicy(s.policy); err != nil {
		return nil, err
	}
	return s, nil
}
func (s *Service) Settings() Policy {
	s.mu.RLock()
	defer s.mu.RUnlock()
	p := s.policy
	p.Exclusions = append([]Exclusion{}, p.Exclusions...)
	return p
}
func (s *Service) SaveSettings(ctx context.Context, input SavePolicy) (Policy, string, error) {
	s.pruneLock.Lock()
	defer s.pruneLock.Unlock()
	input.Exclusions = append([]Exclusion{}, input.Exclusions...)
	if err := validatePolicy(input.Policy); err != nil {
		return Policy{}, "", err
	}
	var saved Policy
	reduce := false
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var row database.MonitoringPolicy
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&row, 1).Error; err != nil {
			return err
		}
		var old Policy
		if err := json.Unmarshal([]byte(row.PolicyJSON), &old); err != nil {
			return err
		}
		if row.Revision != input.Revision {
			return ErrConflict
		}
		reduce = input.RetentionDays < old.RetentionDays
		if reduce && !input.ConfirmRetentionReduction {
			return ErrConfirmation
		}
		saved = input.Policy
		saved.Revision = row.Revision + 1
		raw, _ := json.Marshal(saved)
		return tx.Model(&row).Updates(map[string]any{"policy_json": string(raw), "revision": saved.Revision, "cleanup_pending": row.CleanupPending || reduce, "updated_at": time.Now().UTC()}).Error
	})
	if err != nil {
		return Policy{}, "", err
	}
	s.mu.Lock()
	s.policy = saved
	s.activeSince = time.Now().UTC()
	// In-flight readers are canceled on the next reconciliation; reject their samples immediately.
	s.live = map[string]latest{}
	for id, w := range s.workers {
		w.cancel()
		delete(s.workers, id)
	}
	s.mu.Unlock()
	taskID := ""
	if reduce {
		taskID, _ = s.startCleanup()
	}
	return saved, taskID, nil
}
func (s *Service) Start() {
	s.mu.Lock()
	if s.cancel != nil {
		s.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	s.wg.Add(1)
	s.mu.Unlock()
	go func() {
		defer s.wg.Done()
		tick := s.deps.Tick
		if tick <= 0 {
			tick = time.Second
		}
		timer := time.NewTicker(tick)
		defer timer.Stop()
		s.reconcile(ctx)
		lastMaintenance := time.Time{}
		for {
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
				s.reconcile(ctx)
				if time.Since(lastMaintenance) > time.Hour {
					// Short isolated maintenance never blocks collection.
					lastMaintenance = time.Now()
					s.wg.Add(1)
					go func() {
						defer s.wg.Done()
						_ = s.EnsurePartitions(ctx, time.Now().UTC())
						_ = s.markScheduledCleanup(ctx)
						_, _ = s.startCleanup()
					}()
				}
			}
		}
	}()
}
func (s *Service) Stop() {
	s.mu.Lock()
	if s.cancel != nil {
		s.cancel()
		s.cancel = nil
	}
	for _, w := range s.workers {
		w.cancel()
	}
	s.workers = map[string]worker{}
	s.mu.Unlock()
	s.wg.Wait()
}
func (s *Service) reconcile(ctx context.Context) {
	if s.deps.Nodes == nil || ctx.Err() != nil {
		return
	}
	check, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	nodes, err := s.deps.Nodes(check)
	if err != nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	seen := map[string]bool{}
	for _, n := range nodes {
		seen[n.ID] = true
		policy := s.policy
		if excluded(policy, n, Inventory{}) {
			if w, ok := s.workers[n.ID]; ok {
				w.cancel()
				delete(s.workers, n.ID)
			}
			continue
		}
		if w, ok := s.workers[n.ID]; ok && w.key == n.RuntimeKey {
			select {
			case <-w.done:
			default:
				continue
			}
		}
		if w, ok := s.workers[n.ID]; ok {
			w.cancel()
		}
		run, cancel := context.WithCancel(ctx)
		done := make(chan struct{})
		s.workers[n.ID] = worker{key: n.RuntimeKey, cancel: cancel, done: done}
		s.wg.Add(1)
		go func(n Node) { defer s.wg.Done(); defer close(done); s.runNode(run, n, done) }(n)
	}
	for id, w := range s.workers {
		if !seen[id] {
			w.cancel()
			delete(s.workers, id)
		}
	}
}
func (s *Service) setError(nodeID, message string) {
	s.mu.Lock()
	v := s.statuses[nodeID]
	v.NodeID = nodeID
	v.Error = message
	s.statuses[nodeID] = v
	s.mu.Unlock()
}
func (s *Service) runNode(ctx context.Context, n Node, owner chan struct{}) {
	// One event stream and at most 100 stats streams leave 27 Agent channels free.
	type stream struct {
		cancel     context.CancelFunc
		meta       Inventory
		generation string
	}
	streams := map[string]stream{}
	var wg sync.WaitGroup
	defer func() {
		for _, v := range streams {
			v.cancel()
		}
		wg.Wait()
		s.mu.Lock()
		for containerID, own := range streams {
			id := InstanceID(n.ID, n.EngineID, containerID)
			if v, ok := s.live[id]; ok && v.owner == own.generation {
				delete(s.live, id)
			}
		}
		if w, ok := s.workers[n.ID]; !ok || w.done == owner {
			v := s.statuses[n.ID]
			v.Streams = 0
			s.statuses[n.ID] = v
		}
		s.mu.Unlock()
	}()
	runtime, err := s.deps.Runtime(ctx, n.ID)
	if err != nil {
		s.setError(n.ID, "runtime unavailable")
		s.waitRetry(ctx)
		return
	}
	opening := make(chan struct{}, 6)
	changes := make(chan domain.ContainerEvent, 128)
	wg.Add(1)
	go func() { defer wg.Done(); s.watchEvents(ctx, n, runtime, changes) }()
	tick := s.deps.Tick
	if tick <= 0 {
		tick = time.Second
	}
	timer := time.NewTicker(tick)
	defer timer.Stop()
	lastInventory := time.Time{}
	pending := [][]database.MonitoringSample{}
	for ctx.Err() == nil {
		if time.Since(lastInventory) >= 10*time.Second || s.deps.Tick > 0 && time.Since(lastInventory) >= tick {
			read, cancel := context.WithTimeout(ctx, 5*time.Second)
			rows, e := runtime.MonitoringInventory(read)
			cancel()
			if e != nil {
				s.setError(n.ID, "inventory unavailable")
			} else {
				lastInventory = time.Now()
				seen := map[string]bool{}
				for _, meta := range rows {
					if !fullID.MatchString(meta.ID) || meta.State != "running" || excluded(s.Settings(), n, meta) {
						continue
					}
					seen[meta.ID] = true
					if current, ok := streams[meta.ID]; ok {
						if current.meta.Name != meta.Name || current.meta.Image != meta.Image || current.meta.Project != meta.Project || current.meta.Service != meta.Service {
							_ = s.RecordInstance(ctx, n, meta, time.Now().UTC())
						}
						current.meta = meta
						streams[meta.ID] = current
						continue
					}
					if n.Agent && len(streams) >= 100 {
						s.setError(n.ID, "Agent monitoring stream budget reached")
						continue
					}
					if e := s.RecordInstance(ctx, n, meta, time.Now().UTC()); e != nil {
						s.setError(n.ID, "database unavailable")
						continue
					}
					run, cancel := context.WithCancel(ctx)
					generation := fmt.Sprintf("%d", time.Now().UnixNano())
					streams[meta.ID] = stream{cancel: cancel, meta: meta, generation: generation}
					wg.Add(1)
					go func(meta Inventory, generation string) {
						defer wg.Done()
						s.readStats(run, n, meta, generation, runtime, opening)
					}(meta, generation)
				}
				for id, current := range streams {
					if !seen[id] {
						current.cancel()
						delete(streams, id)
						s.mu.Lock()
						delete(s.live, InstanceID(n.ID, n.EngineID, id))
						s.mu.Unlock()
					}
				}
			}
		}
		now := time.Now().UTC()
		batch := []database.MonitoringSample{}
		for _, v := range streams {
			if excluded(s.Settings(), n, v.meta) {
				continue
			}
			id := InstanceID(n.ID, n.EngineID, v.meta.ID)
			s.mu.Lock()
			current, ok := s.live[id]
			interval := s.policy.IntervalSeconds
			due := now.Sub(current.lastRecorded) >= time.Duration(interval)*time.Second
			if s.deps.Tick > 0 {
				due = now.Sub(current.lastRecorded) >= tick
			}
			if ok && current.owner == v.generation && due && now.Sub(current.received) <= 3*time.Second && (current.previous == nil || current.previous.SampledAt == nil || current.sample.SampledAt.After(*current.previous.SampledAt)) {
				point := makeSample(id, current, now, interval)
				previous := current.sample
				current.previous = &previous
				current.lastRecorded = now
				s.live[id] = current
				batch = append(batch, point)
			}
			st := s.statuses[n.ID]
			st.NodeID = n.ID
			st.Streams = len(streams)
			s.statuses[n.ID] = st
			s.mu.Unlock()
		}
		if len(batch) > 0 {
			pending = append(pending, batch)
		}
		if len(pending) > 12 {
			lost := pending[0]
			pending = pending[1:]
			if len(lost) > 0 {
				_ = s.RecordGap(ctx, n, "", lost[0].RecordedAt, now, "database_buffer_overflow")
			}
		}
		if len(pending) > 0 {
			write, cancel := context.WithTimeout(ctx, 3*time.Second)
			e := s.WriteSamples(write, pending[0])
			cancel()
			if e == nil {
				pending = pending[1:]
				s.mu.Lock()
				st := s.statuses[n.ID]
				at := now
				st.LastWrite = &at
				if st.Error == "database write failed" || st.Error == "database unavailable" {
					st.Error = ""
				}
				s.statuses[n.ID] = st
				s.mu.Unlock()
			} else {
				s.setError(n.ID, "database write failed")
			}
		}
		select {
		case <-ctx.Done():
			return
		case event := <-changes:
			lastInventory = time.Time{}
			if event.Action == "start" || event.Action == "restart" || event.Action == "update" || event.Action == "stop" || event.Action == "die" || event.Action == "destroy" {
				if v, ok := streams[event.ContainerID]; ok {
					v.cancel()
					delete(streams, event.ContainerID)
				}
			}
		case <-timer.C:
		}
	}
}
func (s *Service) waitRetry(ctx context.Context) {
	select {
	case <-ctx.Done():
	case <-time.After(5 * time.Second):
	}
}
func (s *Service) readStats(ctx context.Context, n Node, meta Inventory, generation string, runtime Runtime, opening chan struct{}) {
	backoff := time.Second
	id := InstanceID(n.ID, n.EngineID, meta.ID)
	for ctx.Err() == nil && !excluded(s.Settings(), n, meta) {
		select {
		case opening <- struct{}{}:
		case <-ctx.Done():
			return
		}
		source, err := runtime.NormalizedStats(ctx, meta.ID)
		<-opening
		if err == nil {
			closeOnCancel := context.AfterFunc(ctx, func() { _ = source.Close() })
			decoder := json.NewDecoder(source)
			connectionID := fmt.Sprintf("%s:%d", generation, time.Now().UnixNano())

			for {
				var point domain.Metrics
				if err = decoder.Decode(&point); err != nil {
					break
				}
				if ctx.Err() != nil || !point.Available || point.SampledAt == nil || point.ID != meta.ID {
					continue
				}
				s.mu.Lock()
				v := s.live[id]
				if v.session != connectionID || v.sample.StartedAt == nil || point.StartedAt == nil || !v.sample.StartedAt.Equal(*point.StartedAt) {
					v.previous = nil
				}
				v.sample = point
				v.received = time.Now()
				v.session = connectionID
				v.owner = generation
				v.nodeID = n.ID
				s.live[id] = v
				s.mu.Unlock()
			}
			closeOnCancel()
			_ = source.Close()
			s.mu.Lock()
			if current, ok := s.live[id]; ok && current.owner == generation {
				delete(s.live, id)
			}
			s.mu.Unlock()
			if ctx.Err() == nil {
				_ = s.RecordGap(ctx, n, id, time.Now().UTC(), time.Now().UTC().Add(backoff), "stats_disconnected")
			}
		}
		if ctx.Err() != nil {
			return
		}
		s.setError(n.ID, "stats disconnected; retrying")
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		if backoff < 30*time.Second {
			backoff *= 2
		}
	}
}
func makeSample(id string, v latest, at time.Time, interval int) database.MonitoringSample {
	c := v.sample
	out := database.MonitoringSample{InstanceID: id, RecordedAt: at, SampledAt: *c.SampledAt, Session: v.session, IntervalSeconds: interval, CountersJSON: "{}"}
	if c.StartedAt != nil {
		out.StartedAt = c.StartedAt.UTC().Format(time.RFC3339Nano)
	}
	ptr := func(x float64) *float64 { return &x }
	if c.CPUAvailable {
		out.CPU = ptr(c.CPUPercent)
	}
	if c.MemoryAvailable {
		out.Memory = ptr(float64(c.MemoryBytes))
	}
	if c.MemoryLimitBytes != nil {
		out.MemoryLimit = ptr(float64(*c.MemoryLimitBytes))
	}
	if c.PIDsAvailable {
		out.PIDs = ptr(float64(c.PIDs))
	}
	raw, _ := json.Marshal(map[string]uint64{"network_rx": c.NetworkRXBytes, "network_tx": c.NetworkTXBytes, "block_read": c.BlockReadBytes, "block_write": c.BlockWriteBytes})
	out.CountersJSON = string(raw)
	prev := v.previous
	if prev == nil || c.StartedAt == nil || prev.StartedAt == nil || !c.StartedAt.Equal(*prev.StartedAt) || prev.SampledAt == nil {
		return out
	}
	dt := c.SampledAt.Sub(*prev.SampledAt).Seconds()
	if dt <= 0 || dt > float64(interval)*2.5 {
		return out
	}
	rate := func(a, b uint64) *float64 {
		if a < b {
			return nil
		}
		return ptr(float64(a-b) / dt)
	}
	if c.NetworkAvailable && prev.NetworkAvailable {
		out.NetworkRX = rate(c.NetworkRXBytes, prev.NetworkRXBytes)
		out.NetworkTX = rate(c.NetworkTXBytes, prev.NetworkTXBytes)
	}
	if c.BlockAvailable && prev.BlockAvailable {
		out.BlockRead = rate(c.BlockReadBytes, prev.BlockReadBytes)
		out.BlockWrite = rate(c.BlockWriteBytes, prev.BlockWriteBytes)
	}
	return out
}
func (s *Service) Latest(nodeID, engineID, containerID string) (domain.Metrics, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.live[InstanceID(nodeID, engineID, containerID)]
	return v.sample, ok && s.policy.Enabled && time.Since(v.received) < 3*time.Second
}
func (s *Service) Status() Status {
	s.mu.RLock()
	out := Status{Policy: s.policy, Nodes: []NodeStatus{}}
	out.Policy.Exclusions = append([]Exclusion{}, s.policy.Exclusions...)
	fresh := map[string]int{}
	for _, v := range s.live {
		if time.Since(v.received) < 3*time.Second && v.sample.Available {
			fresh[v.nodeID]++
		}
	}
	for _, v := range s.statuses {
		current := min(v.Streams, fresh[v.NodeID])
		out.ActiveContainers += current
		if current < v.Streams && v.Error == "" {
			v.Error = "some stats samples unavailable"
		}
		if current == v.Streams && v.Error == "stats disconnected; retrying" {
			v.Error = ""
		}
		out.Nodes = append(out.Nodes, v)
	}
	if s.policy.IntervalSeconds > 0 {
		out.EstimatedDailyRecords = int64(out.ActiveContainers) * 86400 / int64(s.policy.IntervalSeconds)
	}
	s.mu.RUnlock()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var row database.MonitoringPolicy
	if s.db.WithContext(ctx).Select("cleanup_task_id").First(&row, 1).Error == nil {
		out.CleanupTaskID = row.CleanupTaskID
	}
	return out
}
