package auto

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
)

type Manager struct {
	mu         sync.Mutex
	dir        string
	config     Config
	tasks      map[string]*Task
	order      []string
	transport  ChatTransport
	checkModel func(string) error
	active     map[string]context.CancelFunc
	ctx        context.Context
	cancel     context.CancelFunc
	wake       chan struct{}
	wg         sync.WaitGroup
	closed     bool
	diskErr    error
}

func New(dir string, transport ChatTransport, validate ...func(string) error) (*Manager, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	state, err := loadState(dir)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	m := &Manager{dir: dir, config: normalizeConfig(state.Config), tasks: map[string]*Task{}, transport: transport,
		active: map[string]context.CancelFunc{}, ctx: ctx, cancel: cancel, wake: make(chan struct{}, 1)}
	if len(validate) > 0 {
		m.checkModel = validate[0]
	}
	for _, t := range state.Tasks {
		if t == nil || t.ID == "" || m.tasks[t.ID] != nil {
			cancel()
			return nil, errors.New("invalid persisted Auto task")
		}
		if t.RouteScores == nil {
			t.RouteScores = map[string]float64{}
		}
		m.tasks[t.ID], m.order = t, append(m.order, t.ID)
		for i := range t.Runs {
			if activeStatus(t.Runs[i].Status) {
				t.Runs[i].Status, t.Runs[i].Error = "interrupted", "Magpie restarted; Continue starts a fresh bounded request"
				t.Runs[i].Finished = time.Now().UTC()
			}
		}
		if activeStatus(t.Status) {
			t.Status = "interrupted"
		}
	}
	if err = m.replay(); err == nil {
		err = m.saveLocked()
	}
	if err != nil {
		cancel()
		return nil, err
	}
	m.wg.Add(1)
	go m.schedule()
	return m, nil
}
func normalizeConfig(c Config) Config {
	c.MaxConcurrent = min(4, max(1, c.MaxConcurrent))
	if c.ContextBytes == 0 {
		c.ContextBytes = 16000
	}
	return c
}
func validateConfig(c Config) error {
	if c.ContextBytes < 1024 || c.ContextBytes > 64000 || len(c.Routes) > 16 {
		return errors.New("contextBytes must be 1024–64000 and at most 16 routes may be configured")
	}
	seen := map[string]bool{}
	for _, r := range c.Routes {
		if r.ID == "" || len(r.ID) > 80 || seen[r.ID] || r.Adapter != "gateway" || r.Model == "" || len(r.Label)+len(r.Description) > 300 || len(r.Efforts) > 8 {
			return errors.New("routes require unique IDs, supported adapters, a configured model and short role metadata")
		}
		if r.AllowWrites || r.Workspace != "" {
			return errors.New("Magpie provides inference; dsh owns workspace and tool permissions")
		}
		seen[r.ID] = true
		for i, e := range r.Efforts {
			if !slices.Contains([]string{"none", "minimal", "low", "medium", "high", "xhigh", "max"}, e) || slices.Contains(r.Efforts[:i], e) {
				return errors.New("route efforts must be unique supported effort names")
			}
		}
	}
	return nil
}
func preferencesValid(p Preferences) bool {
	for _, v := range []float64{p.Quality, p.Cost, p.Speed} {
		if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 || v > 1 {
			return false
		}
	}
	return true
}
func (m *Manager) availableLocked() error {
	if m.closed {
		return errors.New("Auto manager has stopped")
	}
	return m.diskErr
}
func (m *Manager) configuredLocked() error {
	if err := validateConfig(m.config); err != nil {
		return err
	}
	if m.config.Decider != JevFree || m.config.JevKeyFile == "" || len(m.config.Routes) == 0 || m.transport == nil {
		return errors.New("Auto is unconfigured: select Jev Free, a Console key file, and at least one inference route")
	}
	if m.checkModel != nil {
		for _, model := range routeModels(m.config.Routes) {
			if err := m.checkModel(model); err != nil {
				return fmt.Errorf("Auto model %q is unavailable: %w", model, err)
			}
		}
	}
	return m.availableLocked()
}
func routeModels(routes []Route) []string {
	var models []string
	for _, route := range routes {
		models = append(models, route.Model)
	}
	return models
}
func (m *Manager) Config() Config { m.mu.Lock(); defer m.mu.Unlock(); return clone(m.config) }
func (m *Manager) SetConfig(c Config) error {
	c = normalizeConfig(c)
	for i := range c.Routes {
		if c.Routes[i].Adapter == "workspace" && filepath.IsAbs(c.Routes[i].Workspace) {
			resolved, err := filepath.EvalSymlinks(c.Routes[i].Workspace)
			if err != nil {
				return err
			}
			c.Routes[i].Workspace = resolved
		}
	}
	if err := validateConfig(c); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.availableLocked(); err != nil {
		return err
	}
	if len(m.active) != 0 || slices.ContainsFunc(m.order, func(id string) bool { return activeStatus(m.tasks[id].Status) }) {
		return errors.New("stop or finish queued and active requests before changing Auto configuration")
	}
	previous := m.config
	m.config = clone(c)
	if err := m.saveLocked(); err != nil {
		m.config = previous
		return err
	}
	return nil
}
func (m *Manager) Snapshot() Snapshot {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := Snapshot{Config: clone(m.config), Tasks: []TaskSummary{}, Configured: true}
	if err := m.configuredLocked(); err != nil {
		s.Configured, s.Error = false, err.Error()
	}
	for i := len(m.order) - 1; i >= 0; i-- {
		t := m.tasks[m.order[i]]
		s.Tasks = append(s.Tasks, TaskSummary{t.ID, bounded(t.Goal, 240), t.Status, t.Updated})
	}
	return s
}
func (m *Manager) Create(in CreateInput) (Task, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.availableLocked(); err != nil {
		return Task{}, err
	}
	if strings.TrimSpace(in.Goal) == "" || len(in.Goal) > 64000 || !preferencesValid(in.Preferences) || len(in.Dependencies) > 8 {
		return Task{}, errors.New("a bounded goal, valid preferences and at most eight dependencies are required")
	}
	if in.ParentID != "" && m.tasks[in.ParentID] == nil {
		return Task{}, errors.New("parent task does not exist")
	}
	for i, id := range in.Dependencies {
		if m.tasks[id] == nil || slices.Contains(in.Dependencies[:i], id) {
			return Task{}, errors.New("dependencies must be unique existing tasks")
		}
	}
	now := time.Now().UTC()
	t := &Task{ID: newID(), Goal: in.Goal, ParentID: in.ParentID, Dependencies: slices.Clone(in.Dependencies),
		Status: "idle", Preferences: in.Preferences, RouteScores: map[string]float64{}, Created: now, Updated: now}
	m.tasks[t.ID], m.order = t, append(m.order, t.ID)
	if err := m.eventLocked(t, Event{Kind: "created", Text: in.Goal}); err != nil {
		return Task{}, err
	}
	return clone(*t), m.saveLocked()
}
func (m *Manager) Task(id string) (Task, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t := m.tasks[id]
	if t == nil {
		return Task{}, errors.New("task does not exist")
	}
	m.refreshLocked(t)
	return clone(*t), nil
}
func (m *Manager) Start(id string, r RunRequest) (Task, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.configuredLocked(); err != nil {
		return Task{}, err
	}
	if !m.config.Enabled {
		return Task{}, errors.New("Auto is disabled")
	}
	t := m.tasks[id]
	if t == nil || activeStatus(t.Status) {
		return Task{}, errors.New("task is missing or already has a queued/active request")
	}
	if strings.TrimSpace(r.Message) == "" {
		r.Message = t.Goal
	}
	if r.Preferences != nil && !preferencesValid(*r.Preferences) {
		return Task{}, errors.New("preferences must be weights between 0 and 1")
	}
	if len(candidates(m.config, r)) == 0 {
		return Task{}, errors.New("no configured route supports the requested route/effort")
	}
	if err := checkBudget(*t, r, m.config.ContextBytes); err != nil {
		return Task{}, err
	}
	if r.Preferences != nil {
		t.Preferences = *r.Preferences
	}
	run := Run{ID: newID(), Status: "queued", Message: r.Message, Request: clone(r), Decider: m.config.Decider, Started: time.Now().UTC()}
	t.Runs, t.Status = append(t.Runs, run), "queued"
	if err := m.eventLocked(t, Event{RunID: run.ID, Kind: "user", Text: r.Message}); err != nil {
		return Task{}, err
	}
	if err := m.saveLocked(); err != nil {
		return Task{}, err
	}
	m.signal()
	return clone(*t), nil
}
func (m *Manager) Continue(id string, r RunRequest) (Task, error) { return m.Start(id, r) }
func (m *Manager) Cancel(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	t := m.tasks[id]
	if t == nil || !activeStatus(t.Status) {
		return errors.New("task has no queued or active request")
	}
	run := latest(t)
	if cancel := m.active[id]; cancel != nil {
		t.Status, run.Status = "stopping", "stopping"
		cancel()
	} else {
		t.Status, run.Status, run.Finished = "cancelled", "cancelled", time.Now().UTC()
	}
	if err := m.eventLocked(t, Event{RunID: run.ID, Kind: "status", Text: t.Status}); err != nil {
		return err
	}
	m.signal()
	return m.saveLocked()
}
func (m *Manager) Feedback(id string, in FeedbackInput) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.availableLocked(); err != nil {
		return err
	}
	t := m.tasks[id]
	if t == nil || math.IsNaN(in.Rating) || math.IsInf(in.Rating, 0) || math.Abs(in.Rating) > 1 || len(in.Text) > 8000 || (in.Preferences != nil && !preferencesValid(*in.Preferences)) {
		return errors.New("feedback requires an existing task, rating from -1 to 1 and bounded text/preferences")
	}
	idx := slices.IndexFunc(t.Runs, func(r Run) bool { return r.ID == in.RunID })
	if idx < 0 || t.Runs[idx].Decision == nil || activeStatus(t.Runs[idx].Status) {
		return errors.New("feedback requires a finished run with an actual route decision")
	}
	if in.Preferences != nil {
		t.Preferences = *in.Preferences
	}
	route := t.Runs[idx].Decision.RouteID
	t.RouteScores[route] = min(8, max(-8, t.RouteScores[route]+in.Rating))
	if err := m.eventLocked(t, Event{RunID: in.RunID, Kind: "feedback", Text: fmt.Sprintf("rating=%g: %s", in.Rating, in.Text)}); err != nil {
		return err
	}
	return m.saveLocked()
}
func (m *Manager) signal() {
	select {
	case m.wake <- struct{}{}:
	default:
	}
}
func (m *Manager) schedule() {
	defer m.wg.Done()
	for {
		select {
		case <-m.ctx.Done():
			return
		case <-m.wake:
		}
		m.mu.Lock()
		for _, id := range m.order {
			t := m.tasks[id]
			if m.closed || m.diskErr != nil || t.Status != "queued" || len(m.active) >= m.config.MaxConcurrent {
				continue
			}
			m.refreshLocked(t)
			failed := slices.ContainsFunc(t.State.PendingDependencies, func(s StateRecord) bool {
				return slices.Contains([]string{"failed", "cancelled", "interrupted"}, s.Status)
			})
			if failed {
				run := latest(t)
				t.Status, run.Status, run.Error, run.Finished = "failed", "failed", "a dependency did not complete; continue it before retrying this task", time.Now().UTC()
				_ = m.eventLocked(t, Event{RunID: run.ID, Kind: "status", Text: run.Error})
				_ = m.saveLocked()
				continue
			}
			if len(t.State.PendingDependencies) > 0 {
				continue
			}
			ctx, cancel := context.WithTimeout(m.ctx, 5*time.Minute)
			m.active[id] = cancel
			t.Status, latest(t).Status = "deciding", "deciding"
			if err := m.saveLocked(); err != nil {
				cancel()
				delete(m.active, id)
				t.Status, latest(t).Status, latest(t).Error = "failed", "failed", err.Error()
				continue
			}
			m.wg.Add(1)
			go m.execute(ctx, clone(m.config), clone(*t))
		}
		m.mu.Unlock()
	}
}
func (m *Manager) execute(ctx context.Context, c Config, task Task) {
	defer m.wg.Done()
	run := latest(&task)
	sources := offeredSources(task, c.ContextBytes-len(run.Request.Message))
	var result ChatResult
	decisionCtx, cancel := context.WithTimeout(ctx, time.Minute)
	d, decisionResult, err := jevDecision(decisionCtx, c, task, run.Request, sources)
	cancel()

	if err == nil && ctx.Err() == nil {
		route := c.Routes[slices.IndexFunc(c.Routes, func(r Route) bool { return r.ID == d.RouteID })]
		contextText, ids := executionPrompt(task, run.Request, route, d, sources, c.ContextBytes)
		if len(contextText) > c.ContextBytes {
			err = errors.New("execution context exceeds contextBytes")
		} else {
			m.mu.Lock()
			actual := latest(m.tasks[task.ID])
			actual.Decision, actual.Route, actual.Context, actual.ContextSourceIDs = &d, route, contextText, ids
			actual.DecisionUsage = decisionResult.Usage
			if ctx.Err() == nil {
				actual.Status, m.tasks[task.ID].Status = "running", "running"
				err = m.saveLocked()
			} else {
				err = ctx.Err()
			}
			m.mu.Unlock()
			if err == nil {
				emit := func(kind, text string) {
					m.mu.Lock()
					writeErr := m.eventLocked(m.tasks[task.ID], Event{RunID: run.ID, Kind: kind, Text: text})
					if writeErr != nil {
						m.active[task.ID]()
					}
					m.mu.Unlock()
				}
				result, err = m.transport(ctx, route.Model, d.Effort, contextText, emit)
			}
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	t := m.tasks[task.ID]
	actual := latest(t)
	actual.Status = "completed"
	if err != nil {
		actual.Status, actual.Error = "failed", err.Error()
	}
	if ctx.Err() != nil {
		actual.Status, actual.Error = "cancelled", ctx.Err().Error()
	}
	if m.diskErr != nil {
		actual.Status, actual.Error = "failed", m.diskErr.Error()
	}
	actual.Usage, actual.DecisionUsage, actual.RequestID = result.Usage, decisionResult.Usage, result.RequestID
	actual.DecisionRequestID = decisionResult.RequestID
	actual.Calls = result.Calls
	actual.Finished, t.Status = time.Now().UTC(), actual.Status
	if result.Text != "" {
		_ = m.eventLocked(t, Event{RunID: actual.ID, Kind: "assistant", Text: result.Text})
	}
	_ = m.eventLocked(t, Event{RunID: actual.ID, Kind: "status", Text: actual.Status + ": " + actual.Error})
	if m.diskErr != nil {
		actual.Status, t.Status, actual.Error = "failed", "failed", m.diskErr.Error()
	}
	m.active[task.ID]()
	delete(m.active, task.ID)
	_ = m.saveLocked()
	if m.diskErr != nil {
		actual.Status, t.Status, actual.Error = "failed", "failed", m.diskErr.Error()
	}
	m.signal()
}
func (m *Manager) Close() error {
	m.mu.Lock()
	m.closed = true
	m.cancel()
	m.mu.Unlock()
	m.wg.Wait()
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.diskErr
}
