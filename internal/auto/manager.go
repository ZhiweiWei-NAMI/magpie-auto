package auto

import (
	"context"
	"encoding/json"
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

type ownedExecution struct {
	RunID  string
	Cancel context.CancelFunc
}
type actionResult struct {
	Response ActionResponse
	Err      error
}
type pendingAction struct {
	Request ActionRequest
	Reply   chan actionResult
}
type Manager struct {
	mu            sync.Mutex
	dir           string
	config        Config
	tasks         map[string]*Task
	order         []string
	executor      Executor
	active        map[string]ownedExecution
	pending       map[string]*pendingAction
	secrets       map[string][]string
	privateOutput map[string]string
	wg            sync.WaitGroup
	closed        bool
	decision      func(context.Context, Config, Task, RunRequest) (Decision, error)
}

// UseDecisionTransport binds Auto to the owning gateway's decision metering.
func (m *Manager) UseDecisionTransport(transport DecisionTransport) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if transport == nil {
		m.decision = decide
		return
	}
	m.decision = func(ctx context.Context, c Config, t Task, r RunRequest) (Decision, error) {
		return decideWithTransport(ctx, c, t, r, transport)
	}
}
func New(dir string, executor Executor) (*Manager, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	s, err := loadState(dir)
	if err != nil {
		return nil, err
	}
	m := &Manager{dir: dir, config: normalizeConfig(s.Config), tasks: map[string]*Task{}, active: map[string]ownedExecution{}, pending: map[string]*pendingAction{}, secrets: map[string][]string{}, privateOutput: map[string]string{}, executor: executor, decision: decide}
	for _, t := range s.Tasks {
		if t == nil || t.ID == "" {
			return nil, errors.New("invalid persisted Auto task")
		}
		if t.RouteScores == nil {
			t.RouteScores = map[string]float64{}
		}
		m.tasks[t.ID] = t
		m.order = append(m.order, t.ID)
	}
	if err = m.replayLocked(); err != nil {
		return nil, err
	}
	for _, t := range m.tasks {
		t.PendingActions = []ActionRequest{}
		for i := range t.Runs {
			r := &t.Runs[i]
			if isActiveStatus(r.Status) {
				r.Status = "interrupted"
				r.Error = "Magpie restarted; pending actions expired; continue to create a fresh bounded execution"
				if latestRun(t) == r {
					t.Status = "interrupted"
				}
			}
		}
		for i := range t.Sources {
			source := &t.Sources[i]
			if source.TaskID == "" {
				source.TaskID = t.ID
			}
			if source.RunID == "" {
				for _, e := range t.Events {
					if e.ID == source.ID {
						source.RunID = e.RunID
						break
					}
				}
			}
		}
	}
	for _, t := range m.tasks {
		m.refreshStateLocked(t)
	}
	if err = m.saveLocked(); err != nil {
		return nil, err
	}
	return m, nil
}
func normalizeConfig(c Config) Config {
	if c.MaxConcurrent < 1 {
		c.MaxConcurrent = 1
	}
	if c.MaxConcurrent > 4 {
		c.MaxConcurrent = 4
	}
	if c.ContextBytes < 1024 {
		c.ContextBytes = 16000
	}
	if c.ContextBytes > 64000 {
		c.ContextBytes = 64000
	}
	for i := range c.Routes {
		if c.Routes[i].Sandbox == "" || c.Routes[i].Sandbox == "default" {
			c.Routes[i].Sandbox = "read-only"
		}
	}
	return c
}
func validatePreferences(p Preferences) error {
	for _, v := range []float64{p.Quality, p.Cost, p.Speed} {
		if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 || v > 1 {
			return errors.New("quality, cost and speed preferences must be finite weights between 0 and 1")
		}
	}
	return nil
}
func validateRoute(r Route) error {
	if r.Adapter != "codex-app-server" {
		return fmt.Errorf("unsupported execution adapter %q", r.Adapter)
	}
	if r.ID == "" || r.Model == "" || r.Command == "" {
		return errors.New("each route needs an ID, model and executable command")
	}
	if !slices.Contains([]string{"", "default", "read-only", "workspace-write"}, r.Sandbox) {
		return errors.New("executor sandbox must be read-only or explicitly workspace-write; full access is unsupported")
	}
	if !filepath.IsAbs(r.CWD) {
		return errors.New("each route needs an existing absolute working directory")
	}
	info, err := os.Stat(r.CWD)
	if err != nil || !info.IsDir() {
		return fmt.Errorf("executor working directory does not exist or is not a directory: %s", r.CWD)
	}
	return nil
}
func (m *Manager) Config() Config { m.mu.Lock(); defer m.mu.Unlock(); return clone(m.config) }
func (m *Manager) SetConfig(c Config) error {
	c = normalizeConfig(c)
	seen := map[string]bool{}
	for _, r := range c.Routes {
		if strings.TrimSpace(r.ID) == "" || seen[r.ID] {
			return errors.New("executor route IDs must be unique and nonempty")
		}
		seen[r.ID] = true
		if err := validateRoute(r); err != nil {
			return err
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return errors.New("Auto manager is closed")
	}
	previous := m.config
	m.config = clone(c)
	if err := m.saveLocked(); err != nil {
		m.config = previous
		return err
	}
	m.scheduleLocked()
	return nil
}
func (m *Manager) Snapshot() Snapshot {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := Snapshot{Config: clone(m.config)}
	for i := len(m.order) - 1; i >= 0; i-- {
		t := m.tasks[m.order[i]]
		m.refreshStateLocked(t)
		s.Tasks = append(s.Tasks, clone(*t))
	}
	if err := configured(m.config); err != nil {
		s.Error = err.Error()
	} else {
		s.Configured = true
	}
	return s
}
func (m *Manager) Task(id string) (Task, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t := m.tasks[id]
	if t == nil {
		return Task{}, errors.New("Auto task not found")
	}
	m.refreshStateLocked(t)
	return clone(*t), nil
}
func (m *Manager) Create(in CreateInput) (Task, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.createLocked(in)
}
func (m *Manager) makeTaskLocked(in CreateInput) (*Task, error) {
	if err := validatePreferences(in.Preferences); err != nil {
		return nil, err
	}
	if m.closed {
		return nil, errors.New("Auto manager is closed")
	}
	in.Goal = strings.TrimSpace(in.Goal)
	if in.Goal == "" || len(in.Goal) > 16000 {
		return nil, errors.New("task goal must contain 1 to 16000 bytes")
	}
	if in.ParentID != "" && m.tasks[in.ParentID] == nil {
		return nil, errors.New("parent task not found")
	}
	for _, id := range in.Dependencies {
		if m.tasks[id] == nil {
			return nil, errors.New("dependency task not found")
		}
	}
	now := time.Now().UTC()
	t := &Task{ID: newID(), Goal: in.Goal, ParentID: in.ParentID, Dependencies: slices.Clone(in.Dependencies), Preferences: in.Preferences, RouteScores: map[string]float64{}, Status: "ready", Created: now, Updated: now, Runs: []Run{}, Events: []Event{}, Sources: []Source{}, Artifacts: []Artifact{}, PendingActions: []ActionRequest{}}
	m.tasks[t.ID] = t
	m.order = append(m.order, t.ID)
	return t, nil
}
func (m *Manager) createLocked(in CreateInput) (Task, error) {
	t, err := m.makeTaskLocked(in)
	if err != nil {
		return Task{}, err
	}
	if err = m.eventLocked(t, Event{Kind: "created", Text: in.Goal}); err != nil {
		return Task{}, err
	}
	return clone(*t), nil
}
func (m *Manager) Start(id string, request RunRequest) (Task, error) {
	if request.Preferences != nil {
		if err := validatePreferences(*request.Preferences); err != nil {
			return Task{}, err
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return Task{}, errors.New("Auto manager is closed")
	}
	if err := configured(m.config); err != nil {
		return Task{}, err
	}
	if m.executor == nil {
		return Task{}, errors.New("Auto execution adapter is unavailable")
	}
	if len(eligible(m.config, request)) == 0 {
		return Task{}, errors.New("no eligible configured executor route")
	}
	t := m.tasks[id]
	if t == nil {
		return Task{}, errors.New("Auto task not found")
	}
	if isActiveStatus(t.Status) {
		return Task{}, errors.New("task already has an active or queued run")
	}
	if request.Message == "" {
		request.Message = t.Goal
	}
	if len(request.Message) > 16000 {
		return Task{}, errors.New("current request exceeds 16000 bytes")
	}
	if err := checkPromptBudget(*t, request, m.config.ContextBytes); err != nil {
		return Task{}, err
	}
	run := Run{ID: newID(), Status: "queued", Message: request.Message, Request: clone(request), Started: time.Now().UTC()}
	t.Runs = append(t.Runs, run)
	t.Status = "queued"
	m.refreshDependencySourcesLocked(t)
	if err := m.eventLocked(t, Event{RunID: run.ID, Kind: "user", Text: request.Message}); err != nil {
		t.Status = "failed"
		latestRun(t).Status = "failed"
		latestRun(t).Error = err.Error()
		return Task{}, err
	}
	m.scheduleLocked()
	return clone(*t), nil
}
func (m *Manager) Continue(id string, r RunRequest) (Task, error) { return m.Start(id, r) }
func (m *Manager) readyLocked(t *Task) bool {
	for _, id := range t.Dependencies {
		dep := m.tasks[id]
		if dep == nil || dep.Status != "completed" {
			return false
		}
	}
	return true
}
func (m *Manager) scheduleLocked() {
	if m.closed || !m.config.Enabled || m.executor == nil {
		return
	}
	for _, id := range m.order {
		if len(m.active) >= m.config.MaxConcurrent {
			return
		}
		t := m.tasks[id]
		run := latestRun(t)
		if t.Status != "queued" || run == nil || run.Status != "queued" || !m.readyLocked(t) {
			continue
		}
		m.refreshDependencySourcesLocked(t)
		ctx, cancel := context.WithCancel(context.Background())
		m.active[id] = ownedExecution{RunID: run.ID, Cancel: cancel}
		run.Status = "deciding"
		t.Status = "deciding"
		text := "Asking Jev for a typed execution decision"
		if run.ParentRunID != "" && run.Decision != nil {
			run.Status = "running"
			t.Status = "running"
			text = "Executing the parent's validated decision"
		}
		if err := m.eventLocked(t, Event{RunID: run.ID, Kind: "status", Text: text}); err != nil {
			cancel()
			delete(m.active, id)
			run.Status = "failed"
			run.Error = err.Error()
			t.Status = "failed"
			m.syncParentCompletionLocked(t, run)
			continue
		}
		task, config, request, runID := clone(*t), clone(m.config), clone(run.Request), run.ID
		m.wg.Add(1)
		go m.execute(ctx, task, config, request, runID)
	}
}
func (m *Manager) execute(ctx context.Context, task Task, config Config, request RunRequest, runID string) {
	defer m.wg.Done()
	m.mu.Lock()
	decisionFunc := m.decision
	m.mu.Unlock()
	snapshotRun := findRun(&task, runID)
	var decision Decision
	var err error
	if snapshotRun != nil && snapshotRun.ParentRunID != "" && snapshotRun.Decision != nil {
		decision = *snapshotRun.Decision
	} else {
		decision, err = decisionFunc(ctx, config, task, request)
	}
	if err != nil {
		m.finish(task.ID, runID, RunResult{}, err)
		return
	}
	m.mu.Lock()
	t := m.tasks[task.ID]
	run := findRun(t, runID)
	actor, owned := m.active[task.ID]
	if run == nil || !owned || actor.RunID != runID || latestRun(t) != run {
		m.mu.Unlock()
		return
	}
	if ctx.Err() != nil {
		m.mu.Unlock()
		m.finish(task.ID, runID, RunResult{}, ctx.Err())
		return
	}
	routeIndex := slices.IndexFunc(config.Routes, func(r Route) bool { return r.ID == decision.RouteID })
	route := Route{}
	if run.ParentRunID != "" {
		route = run.Route
	} else if routeIndex >= 0 {
		route = config.Routes[routeIndex]
	}
	if err = validateBoundDecision(route, request, decision); err != nil {
		m.mu.Unlock()
		m.finish(task.ID, runID, RunResult{}, err)
		return
	}
	run.Decision = &decision
	run.Route = clone(route)
	if decision.Action == "child" {
		err = m.handoffChildLocked(t, run, decision, request, config.ContextBytes)
		m.mu.Unlock()
		if err != nil {
			m.finish(task.ID, runID, RunResult{}, err)
		}
		return
	}
	if run.ParentRunID == "" {
		run.Context, run.ContextSourceIDs = assembleContext(*t, request, decision, config.ContextBytes)
	}
	if len(run.Context) > config.ContextBytes {
		m.mu.Unlock()
		m.finish(task.ID, runID, RunResult{}, errors.New("current request no longer fits the configured context budget"))
		return
	}
	run.Status = "running"
	t.Status = "running"
	err = m.eventLocked(t, Event{RunID: runID, Kind: "decision", Text: decision.Reason})
	input := RunInput{TaskID: task.ID, RunID: runID, Prompt: run.Context, Route: clone(route), Effort: decision.Effort, RequestAction: func(actionCtx context.Context, a ActionRequest) (ActionResponse, error) {
		return m.requestAction(actionCtx, task.ID, runID, a)
	}}
	m.mu.Unlock()
	if err != nil {
		m.finish(task.ID, runID, RunResult{}, err)
		return
	}
	result, err := m.executor.Run(ctx, input, func(e Event) {
		m.mu.Lock()
		defer m.mu.Unlock()
		actor, ok := m.active[task.ID]
		if !ok || actor.RunID != runID {
			return
		}
		if saveErr := m.emitOwnedLocked(task.ID, runID, e); saveErr != nil {
			actor.Cancel()
		}
	})
	m.finish(task.ID, runID, result, err)
}
func validateBoundDecision(route Route, request RunRequest, d Decision) error {
	if err := validateRoute(route); err != nil {
		return err
	}
	if route.ID != d.RouteID || request.RouteID != "" && request.RouteID != route.ID {
		return errors.New("selected route violates the current request")
	}
	if request.Effort != "" && request.Effort != d.Effort {
		return errors.New("selected effort violates the current request")
	}
	if len(route.Efforts) > 0 && !slices.Contains(route.Efforts, d.Effort) {
		return errors.New("selected effort is unsupported by the executor route")
	}
	if len(route.Efforts) == 0 && d.Effort != "" {
		return errors.New("executor route only supports default effort")
	}
	if d.Action != "execute" && d.Action != "child" {
		return errors.New("invalid execution action")
	}
	return nil
}
func (m *Manager) handoffChildLocked(parent *Task, parentRun *Run, d Decision, r RunRequest, limit int) error {
	child, err := m.makeTaskLocked(CreateInput{Goal: r.Message, ParentID: parent.ID, Preferences: parent.Preferences})
	if err != nil {
		return err
	}
	inheritChildContext(child, *parent, d, r)
	childDecision := clone(d)
	childDecision.Action = "execute"
	goalID := "task:" + parent.ID + ":goal"
	childDecision.SourceIDs = append(childDecision.SourceIDs, goalID)
	childRun := Run{ID: newID(), Status: "queued", Message: r.Message, Request: clone(r), Decision: &childDecision, Route: clone(parentRun.Route), ParentTaskID: parent.ID, ParentRunID: parentRun.ID, Started: time.Now().UTC()}
	childRun.Context, childRun.ContextSourceIDs = assembleContext(*child, r, childDecision, limit)
	if len(childRun.Context) > limit {
		delete(m.tasks, child.ID)
		m.order = m.order[:len(m.order)-1]
		return errors.New("current child request exceeds the configured context budget")
	}
	child.Runs = append(child.Runs, childRun)
	child.Status = "queued"
	parentRun.ChildTaskID = child.ID
	parentRun.ChildRunID = childRun.ID
	parentRun.Context = childRun.Context
	parentRun.ContextSourceIDs = slices.Clone(childRun.ContextSourceIDs)
	parentRun.Status = "waiting"
	parent.Status = "waiting"
	if err = m.eventLocked(child, Event{Kind: "created", Text: child.Goal}); err == nil {
		err = m.eventLocked(child, Event{RunID: childRun.ID, Kind: "user", Text: r.Message})
	}
	if err == nil {
		err = m.eventLocked(parent, Event{RunID: parentRun.ID, Kind: "child", Text: child.ID + "/" + childRun.ID})
	}
	if err != nil {
		child.Status = "failed"
		latestRun(child).Status = "failed"
		return err
	}
	if actor, ok := m.active[parent.ID]; ok && actor.RunID == parentRun.ID {
		delete(m.active, parent.ID)
		actor.Cancel()
	}
	m.scheduleLocked()
	return nil
}
func inheritChildContext(child *Task, parent Task, d Decision, r RunRequest) {
	child.Summary = parent.Summary
	child.RouteScores = clone(parent.RouteScores)
	child.Sources = append(child.Sources, Source{ID: "task:" + parent.ID + ":goal", Kind: "parent_goal", Text: parent.Goal, TaskID: parent.ID})
	for _, s := range parent.Sources {
		if slices.Contains(d.SourceIDs, s.ID) {
			child.Sources = append(child.Sources, s)
		}
	}
	if r.Preferences != nil {
		child.Preferences = *r.Preferences
	}
}
func (m *Manager) linkedParentLocked(child *Task, run *Run) (*Task, *Run) {
	if run == nil || run.ParentTaskID == "" {
		return nil, nil
	}
	parent := m.tasks[run.ParentTaskID]
	pr := findRun(parent, run.ParentRunID)
	if pr == nil || latestRun(parent) != pr || pr.ChildTaskID != child.ID || pr.ChildRunID != run.ID || !isActiveStatus(pr.Status) {
		return nil, nil
	}
	return parent, pr
}
func (m *Manager) emitOwnedLocked(taskID, runID string, e Event) error {
	t := m.tasks[taskID]
	run := findRun(t, runID)
	if run == nil || latestRun(t) != run {
		return nil
	}
	if e.ID == "" {
		e.ID = newID()
	}
	e.RunID = runID
	e.TaskID = taskID
	if len(m.secrets[runID]) > 0 {
		// Do not persist recoverable fragments of a secret split across streamed deltas.
		switch e.Kind {
		case "message_delta":
			m.privateOutput[runID] += e.Text
			e.Text = ""
		case "commentary_delta":
			e.Text = "[Commentary withheld after secret input]"
		case "progress":
			e.Text = "[Progress details withheld after secret input]"
		case "executor_event":
			e.Text = "[Executor payload withheld after secret input]"
		}
	}
	e.Text = m.redactRunTextLocked(runID, e.Text)
	if e.Kind == "session" {
		run.ThreadID = e.Text
	}
	if err := m.eventLocked(t, e); err != nil {
		return err
	}
	if parent, pr := m.linkedParentLocked(t, run); parent != nil && slices.Contains([]string{"session", "progress", "commentary_delta", "message_delta", "assistant", "error", "action_request", "action_response", "action_expired"}, e.Kind) {
		copyEvent := e
		copyEvent.ID = newID()
		copyEvent.RunID = pr.ID
		copyEvent.SourceID = e.ID
		if e.SourceID != "" {
			copyEvent.SourceID = e.SourceID
		}
		copyEvent.OriginTaskID = t.ID
		copyEvent.OriginRunID = runID
		if e.Kind == "session" {
			pr.ThreadID = run.ThreadID
		}
		return m.eventLocked(parent, copyEvent)
	}
	return nil
}
func (m *Manager) finish(taskID, runID string, result RunResult, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	actor, ok := m.active[taskID]
	if !ok || actor.RunID != runID {
		return
	}
	delete(m.active, taskID)
	actor.Cancel()
	t := m.tasks[taskID]
	run := findRun(t, runID)
	if run == nil || latestRun(t) != run {
		return
	}
	m.completeRunLocked(t, run, result, err)
	m.scheduleLocked()
}
func (m *Manager) completeRunLocked(t *Task, run *Run, result RunResult, err error) {
	defer delete(m.secrets, run.ID)
	defer delete(m.privateOutput, run.ID)
	result.ThreadID = m.redactRunTextLocked(run.ID, result.ThreadID)
	if err != nil {
		message := m.redactRunTextLocked(run.ID, err.Error())
		if message != err.Error() {
			if errors.Is(err, context.Canceled) {
				err = fmt.Errorf("%s: %w", message, context.Canceled)
			} else {
				err = errors.New(message)
			}
		}
	}
	m.expireActionsLocked(t.ID, run.ID, context.Canceled)
	if result.ThreadID != "" {
		run.ThreadID = result.ThreadID
	}
	run.Finished = time.Now().UTC()
	run.Status = "completed"
	if err != nil {
		run.Status = "failed"
		run.Error = err.Error()
	}
	if errors.Is(err, context.Canceled) || t.Status == "stopping" {
		run.Status = "cancelled"
	}
	t.Status = run.Status
	var output strings.Builder
	hasFinal := false
	for _, e := range t.Events {
		if e.RunID == run.ID {
			if e.Kind == "message_delta" {
				output.WriteString(e.Text)
			}
			if e.Kind == "assistant" {
				hasFinal = true
			}
		}
	}
	output.WriteString(m.privateOutput[run.ID])
	if output.Len() > 0 && !hasFinal {
		if saveErr := m.emitOwnedLocked(t.ID, run.ID, Event{Kind: "assistant", Text: output.String()}); saveErr != nil {
			run.Error = saveErr.Error()
			run.Status = "failed"
			t.Status = "failed"
		}
	}
	for _, a := range result.Artifacts {
		a.ID = m.redactRunTextLocked(run.ID, a.ID)
		a.Path = m.redactRunTextLocked(run.ID, a.Path)
		a.SourceID = m.redactRunTextLocked(run.ID, a.SourceID)
		if a.ID == "" {
			a.ID = newID()
		}
		a.TaskID = t.ID
		a.RunID = run.ID
		if a.SourceID == "" {
			a.SourceID = newID()
			_ = m.eventLocked(t, Event{ID: a.SourceID, RunID: run.ID, Kind: "artifact", Text: a.Path})
		}
		t.Artifacts = append(t.Artifacts, a)
	}
	for i := len(t.Sources) - 1; i >= 0; i-- {
		if t.Sources[i].Kind == "assistant" && t.Sources[i].RunID == run.ID {
			t.Summary = bounded(t.Sources[i].Text, 2048)
			break
		}
	}
	text := run.Status
	if err != nil {
		text += " : " + err.Error()
	}
	if saveErr := m.eventLocked(t, Event{RunID: run.ID, Kind: "status", Text: text}); saveErr != nil {
		run.Error = "persistence failed: " + saveErr.Error()
		run.Status = "failed"
		t.Status = "failed"
	}
	m.syncParentCompletionLocked(t, run)
}
func (m *Manager) syncParentCompletionLocked(child *Task, run *Run) {
	parent, pr := m.linkedParentLocked(child, run)
	if parent == nil {
		return
	}
	pr.Route = clone(run.Route)
	pr.ThreadID = run.ThreadID
	pr.Context = run.Context
	pr.ContextSourceIDs = slices.Clone(run.ContextSourceIDs)
	pr.Status = run.Status
	pr.Error = run.Error
	pr.Finished = run.Finished
	parent.Status = run.Status
	parent.Summary = child.Summary
	for _, a := range child.Artifacts {
		if a.RunID == run.ID && !slices.ContainsFunc(parent.Artifacts, func(other Artifact) bool { return other.ID == a.ID && other.TaskID == a.TaskID }) {
			parent.Artifacts = append(parent.Artifacts, a)
		}
	}
	m.expireActionsLocked(parent.ID, pr.ID, context.Canceled)
	_ = m.eventLocked(parent, Event{RunID: pr.ID, Kind: "status", Text: "Owned child " + child.ID + "/" + run.ID + " " + run.Status})
}
func (m *Manager) Cancel(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	t := m.tasks[id]
	if t == nil {
		return errors.New("Auto task not found")
	}
	run := latestRun(t)
	if run == nil || !isActiveStatus(run.Status) {
		return errors.New("task has no active execution")
	}
	return m.cancelRunLocked(t, run)
}
func (m *Manager) cancelRunLocked(t *Task, run *Run) error {
	if run.ChildTaskID != "" {
		child := m.tasks[run.ChildTaskID]
		cr := findRun(child, run.ChildRunID)
		if cr == nil || latestRun(child) != cr || cr.ParentTaskID != t.ID || cr.ParentRunID != run.ID || !isActiveStatus(cr.Status) {
			return errors.New("owned child execution is no longer active")
		}
		t.Status = "stopping"
		run.Status = "stopping"
		if err := m.cancelRunLocked(child, cr); err != nil {
			return err
		}
		return m.eventLocked(t, Event{RunID: run.ID, Kind: "status", Text: "Stopping the linked owned child execution"})
	}
	m.expireActionsLocked(t.ID, run.ID, context.Canceled)
	if actor, ok := m.active[t.ID]; ok && actor.RunID == run.ID {
		t.Status = "stopping"
		run.Status = "stopping"
		actor.Cancel()
		if parent, pr := m.linkedParentLocked(t, run); parent != nil {
			parent.Status = "stopping"
			pr.Status = "stopping"
		}
		return m.eventLocked(t, Event{RunID: run.ID, Kind: "status", Text: "Stopping owned execution"})
	}
	if run.Status == "queued" {
		m.completeRunLocked(t, run, RunResult{}, context.Canceled)
		m.scheduleLocked()
		return nil
	}
	return errors.New("task has no active execution")
}
func (m *Manager) Feedback(id string, f FeedbackInput) error {
	if f.Preferences != nil {
		if err := validatePreferences(*f.Preferences); err != nil {
			return err
		}
	}
	if math.IsNaN(f.Rating) || math.IsInf(f.Rating, 0) || f.Rating < -1 || f.Rating > 1 {
		return errors.New("feedback rating must be between -1 and 1")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	t := m.tasks[id]
	run := findRun(t, f.RunID)
	if t == nil {
		return errors.New("Auto task not found")
	}
	if run == nil || run.Route.ID == "" || run.Status != "completed" {
		return errors.New("feedback needs a completed executed run")
	}
	route := run.Route.ID
	if t.RouteScores == nil {
		t.RouteScores = map[string]float64{}
	}
	t.RouteScores[route] = max(-1, min(1, t.RouteScores[route]*0.8+f.Rating*0.2))
	if f.Preferences != nil {
		t.Preferences = *f.Preferences
	}
	return m.eventLocked(t, Event{RunID: f.RunID, Kind: "feedback", Text: fmt.Sprintf("Task-specific route score %.2f; %s", t.RouteScores[route], f.Text)})
}
func (m *Manager) requestAction(ctx context.Context, taskID, runID string, request ActionRequest) (ActionResponse, error) {
	m.mu.Lock()
	t := m.tasks[taskID]
	run := findRun(t, runID)
	actor, ok := m.active[taskID]
	if m.closed || !ok || actor.RunID != runID || run == nil || latestRun(t) != run || run.Status == "stopping" {
		m.mu.Unlock()
		return ActionResponse{}, context.Canceled
	}
	request.ID = newID()
	if m.pending[request.ID] != nil {
		m.mu.Unlock()
		return ActionResponse{}, errors.New("duplicate pending action ID")
	}
	request.TaskID = taskID
	request.RunID = runID
	request.Command = m.redactRunTextLocked(runID, request.Command)
	request.CWD = m.redactRunTextLocked(runID, request.CWD)
	request.Reason = m.redactRunTextLocked(runID, request.Reason)
	request.ThreadID = m.redactRunTextLocked(runID, request.ThreadID)
	request.TurnID = m.redactRunTextLocked(runID, request.TurnID)
	request.ItemID = m.redactRunTextLocked(runID, request.ItemID)
	for i, path := range request.Paths {
		request.Paths[i] = m.redactRunTextLocked(runID, path)
		if path != request.Paths[i] {
			request.AllowedDecisions = slices.DeleteFunc(slices.Clone(request.AllowedDecisions), func(d string) bool { return d == "accept" })
		}
	}
	for i := range request.Questions {
		question := &request.Questions[i]
		question.Header = m.redactRunTextLocked(runID, question.Header)
		question.Question = m.redactRunTextLocked(runID, question.Question)
		for j := range question.Options {
			question.Options[j].Label = m.redactRunTextLocked(runID, question.Options[j].Label)
			question.Options[j].Description = m.redactRunTextLocked(runID, question.Options[j].Description)
		}
	}
	if !slices.Contains([]string{"command", "file", "input"}, request.Kind) {
		m.mu.Unlock()
		return ActionResponse{}, errors.New("unsupported executor action")
	}
	if request.Kind == "command" || request.Kind == "file" && (run.Route.Sandbox != "workspace-write" || len(request.Paths) == 0) {
		request.AllowedDecisions = slices.DeleteFunc(slices.Clone(request.AllowedDecisions), func(d string) bool { return d == "accept" })
	}
	request.AllowedDecisions = slices.DeleteFunc(slices.Clone(request.AllowedDecisions), func(d string) bool { return !slices.Contains([]string{"accept", "decline", "cancel"}, d) })
	pending := &pendingAction{Request: clone(request), Reply: make(chan actionResult, 1)}
	m.pending[request.ID] = pending
	t.PendingActions = append(t.PendingActions, clone(request))
	run.Status = "awaiting_action"
	t.Status = "awaiting_action"
	if parent, pr := m.linkedParentLocked(t, run); parent != nil {
		parent.PendingActions = append(parent.PendingActions, clone(request))
		parent.Status = "awaiting_action"
		pr.Status = "awaiting_action"
	}
	b, _ := json.Marshal(request)
	err := m.emitOwnedLocked(taskID, runID, Event{Kind: "action_request", Text: string(b)})
	m.mu.Unlock()
	if err != nil {
		m.mu.Lock()
		m.removeActionLocked(request.ID)
		m.mu.Unlock()
		return ActionResponse{}, err
	}
	var answer actionResult
	select {
	case <-ctx.Done():
		answer.Err = ctx.Err()
	case answer = <-pending.Reply:
	}
	m.mu.Lock()
	m.removeActionLocked(request.ID)
	if actor, active := m.active[taskID]; active && actor.RunID == runID && run.Status == "awaiting_action" {
		run.Status = "running"
		t.Status = "running"
		if parent, pr := m.linkedParentLocked(t, run); parent != nil {
			parent.Status = "waiting"
			pr.Status = "waiting"
		}
		_ = m.eventLocked(t, Event{RunID: runID, Kind: "status", Text: "Owned execution resumed after action"})
	}
	m.mu.Unlock()
	return answer.Response, answer.Err
}
func (m *Manager) ReplyAction(taskID, actionID string, response ActionResponse) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	pending := m.pending[actionID]
	if pending == nil {
		return errors.New("pending action expired or was already answered")
	}
	request := pending.Request
	owner := m.tasks[request.TaskID]
	run := findRun(owner, request.RunID)
	actor, ok := m.active[request.TaskID]
	if !ok || actor.RunID != request.RunID || run == nil || latestRun(owner) != run || run.Status != "awaiting_action" {
		return errors.New("pending action belongs to a stale execution")
	}
	if taskID != request.TaskID {
		parent, pr := m.linkedParentLocked(owner, run)
		if parent == nil || parent.ID != taskID || pr.ChildTaskID != request.TaskID || pr.ChildRunID != request.RunID {
			return errors.New("pending action does not belong to this task")
		}
	}
	if err := validateActionResponse(request, response); err != nil {
		return err
	}
	// Secrets are registered before the executor can receive and echo the answer.
	for _, question := range request.Questions {
		if !question.IsSecret {
			continue
		}
		for _, value := range response.Answers[question.ID] {
			if value != "" && !slices.Contains(m.secrets[request.RunID], value) {
				m.secrets[request.RunID] = append(m.secrets[request.RunID], value)
			}
		}
	}
	if err := m.emitOwnedLocked(request.TaskID, request.RunID, Event{Kind: "action_response", Text: "Action " + actionID + " decision " + response.Decision}); err != nil {
		return err
	}
	m.removeActionLocked(actionID)
	pending.Reply <- actionResult{Response: clone(response)}
	return nil
}

func (m *Manager) redactRunTextLocked(runID, text string) string {
	values := slices.Clone(m.secrets[runID])
	slices.SortFunc(values, func(a, b string) int { return len(b) - len(a) })
	for _, value := range values {
		text = strings.ReplaceAll(text, value, "[redacted]")
		encoded, _ := json.Marshal(value)
		escaped := string(encoded[1 : len(encoded)-1])
		if escaped != value {
			text = strings.ReplaceAll(text, escaped, "[redacted]")
		}
	}
	return text
}
func validateActionResponse(request ActionRequest, response ActionResponse) error {
	if request.Kind != "input" || response.Decision != "" {
		if !slices.Contains(request.AllowedDecisions, response.Decision) {
			return errors.New("action decision is not allowed")
		}
		if len(response.Answers) != 0 {
			return errors.New("approval actions do not accept input answers")
		}
		return nil
	}
	if len(request.Questions) == 0 || len(response.Answers) != len(request.Questions) {
		return errors.New("provide an answer for each requested input")
	}
	for _, q := range request.Questions {
		answers, ok := response.Answers[q.ID]
		if !ok || len(answers) != 1 || len(answers[0]) == 0 || len(answers[0]) > 16000 {
			return errors.New("each input requires one nonempty answer of at most 16000 bytes")
		}
		if !q.IsOther && len(q.Options) > 0 && !slices.ContainsFunc(q.Options, func(option ActionOption) bool { return option.Label == answers[0] }) {
			return errors.New("input answer is not one of the allowed options")
		}
	}
	return nil
}
func (m *Manager) removeActionLocked(id string) {
	delete(m.pending, id)
	for _, t := range m.tasks {
		t.PendingActions = slices.DeleteFunc(t.PendingActions, func(a ActionRequest) bool { return a.ID == id })
	}
}
func (m *Manager) expireActionsLocked(taskID, runID string, err error) {
	for id, pending := range m.pending {
		r := pending.Request
		if r.TaskID == taskID && r.RunID == runID {
			m.removeActionLocked(id)
			select {
			case pending.Reply <- actionResult{Err: err}:
			default:
			}
			if t := m.tasks[taskID]; t != nil {
				_ = m.eventLocked(t, Event{RunID: runID, Kind: "action_expired", Text: "Action " + id + " expired"})
			}
		}
	}
}
func (m *Manager) Close() error {
	m.mu.Lock()
	m.closed = true
	for id, actor := range m.active {
		m.expireActionsLocked(id, actor.RunID, context.Canceled)
		actor.Cancel()
	}
	m.mu.Unlock()
	m.wg.Wait()
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.saveLocked()
}
