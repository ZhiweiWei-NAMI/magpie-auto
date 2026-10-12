package auto

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/yetone/magpie/internal/provider"
	"github.com/yetone/magpie/internal/testenv"
)

func TestMain(m *testing.M) { testenv.Main(m) }

type recordingExecutor struct{ inputs chan RunInput }

func (e recordingExecutor) Run(ctx context.Context, in RunInput, emit func(Event)) (RunResult, error) {
	e.inputs <- in
	emit(Event{Kind: "assistant", Text: "completed output"})
	return RunResult{ThreadID: "owned-fixture-thread", Artifacts: []Artifact{{Path: "result.txt"}}}, nil
}
func waitTask(t *testing.T, m *Manager, id, status string) Task {
	t.Helper()
	deadline := time.After(10 * time.Second)
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		task, err := m.Task(id)
		if err != nil {
			t.Fatal(err)
		}
		if task.Status == status {
			return task
		}
		select {
		case <-deadline:
			t.Fatalf("task status %s, wanted %s: %+v", task.Status, status, task.Runs)
		case <-ticker.C:
		}
	}
}
func fixtureConfig(t *testing.T, answers func(map[string]any) map[string]any) Config {
	t.Helper()
	testenv.SetHome(t, t.TempDir())
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in map[string]any
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			t.Error(err)
			http.Error(w, "bad body", 400)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"answers": answers(in)})
	}))
	t.Cleanup(up.Close)
	if err := provider.Save(provider.Provider{ID: "auto-jev", Name: "Auto fixture Jev", Key: "fixture-key", Decide: up.URL + "/v1", Models: []string{"jev-latest"}}); err != nil {
		t.Fatal(err)
	}
	return Config{Enabled: true, Decider: "auto-jev/jev-latest", MaxConcurrent: 1, ContextBytes: 1024, Routes: []Route{{ID: "local", Adapter: "codex-app-server", Model: "fixture-model", Command: "fixture-command", CWD: t.TempDir(), Efforts: []string{"low", "high"}}}}
}
func TestTypedDecisionExecutionFeedbackAndContinue(t *testing.T) {
	var scoreSeen bool
	c := fixtureConfig(t, func(in map[string]any) map[string]any {
		questions := in["questions"].(map[string]any)
		route := questions["route"].(map[string]any)["criteria"].(map[string]any)["local"].(string)
		if strings.Contains(route, "score 0.20") {
			scoreSeen = true
		}
		out := map[string]any{"route": map[string]any{"choice": "local"}, "effort": map[string]any{"choice": "low"}, "action": map[string]any{"choice": "execute"}}
		for k := range questions {
			if strings.HasPrefix(k, "source") {
				out[k] = map[string]any{"choice": "include"}
			}
		}
		return out
	})
	e := recordingExecutor{inputs: make(chan RunInput, 2)}
	m, err := New(t.TempDir(), e)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	if err = m.SetConfig(c); err != nil {
		t.Fatal(err)
	}
	task, err := m.Create(CreateInput{Goal: "Write a useful result"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.Start(task.ID, RunRequest{Message: "Current request", RouteID: "local", Effort: "low"}); err != nil {
		t.Fatal(err)
	}
	done := waitTask(t, m, task.ID, "completed")
	input := <-e.inputs
	if len(input.Prompt) > 1024 || !strings.Contains(input.Prompt, "Current request") || input.Effort != "low" {
		t.Fatalf("invalid bounded execution: %+v", input)
	}
	if len(done.Artifacts) != 1 || done.Artifacts[0].RunID != done.Runs[0].ID || done.Artifacts[0].SourceID == "" {
		t.Fatalf("artifact provenance missing: %+v", done.Artifacts)
	}
	if err = m.Feedback(task.ID, FeedbackInput{RunID: done.Runs[0].ID, Rating: 1, Text: "good"}); err != nil {
		t.Fatal(err)
	}
	if _, err = m.Continue(task.ID, RunRequest{Message: "Refine the result"}); err != nil {
		t.Fatal(err)
	}
	done = waitTask(t, m, task.ID, "completed")
	if len(done.Runs) != 2 || !scoreSeen {
		t.Fatalf("feedback did not influence the next Jev request: %+v", done)
	}
}
func TestUnconfiguredAndRestartInterrupted(t *testing.T) {
	m, err := New(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	task, err := m.Create(CreateInput{Goal: "task"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.Start(task.ID, RunRequest{}); err == nil {
		t.Fatal("disabled Auto started")
	}
	m.mu.Lock()
	m.tasks[task.ID].Status = "running"
	m.tasks[task.ID].Runs = []Run{{ID: "saved-run", Status: "running"}}
	err = m.saveLocked()
	m.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	fresh, err := New(m.dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer fresh.Close()
	got, err := fresh.Task(task.ID)
	if err != nil || got.Status != "interrupted" || got.Runs[0].Status != "interrupted" {
		t.Fatalf("restart falsely completed work: %+v %v", got, err)
	}
}
func TestContextIgnoresUnselectedAndKeepsUTF8(t *testing.T) {
	task := Task{Goal: "goal", Sources: []Source{{ID: "old", Text: "must not appear"}, {ID: "selected", Text: strings.Repeat("界", 1000)}}}
	p := contextPrompt(task, RunRequest{Message: "explicit"}, Decision{SourceIDs: []string{"selected"}}, 1024)
	if len(p) > 1024 || strings.Contains(p, "must not appear") || !utf8.ValidString(p) {
		t.Fatal("context bound or selection failed")
	}
}

func TestDecisionRequiresAnswersAndExplicitEffort(t *testing.T) {
	c := fixtureConfig(t, func(map[string]any) map[string]any { return nil })
	task := Task{Goal: "goal", RouteScores: map[string]float64{}}
	cases := []struct {
		name, body string
		request    RunRequest
		want       string
	}{
		{"missing effort", `{"answers":{"route":{"choice":"local"},"action":{"choice":"execute"}}}`, RunRequest{}, "missing effort"},
		{"missing choice", `{"answers":{"route":{"choice":"local"},"effort":{},"action":{"choice":"execute"}}}`, RunRequest{}, "missing effort"},
		{"explicit effort ignored", `{"answers":{"route":{"choice":"local"},"effort":{"choice":""},"action":{"choice":"execute"}}}`, RunRequest{Effort: "high"}, "explicit effort override"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := decideWithTransport(context.Background(), c, task, tc.request, func(context.Context, provider.Provider, string, []byte) ([]byte, error) { return []byte(tc.body), nil })
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("wanted %q, got %v", tc.want, err)
			}
		})
	}
}
func TestPreferencesRejectInvalidWeights(t *testing.T) {
	for _, value := range []float64{-0.01, 1.01, math.NaN(), math.Inf(1)} {
		if err := validatePreferences(Preferences{Quality: value}); err == nil {
			t.Fatalf("invalid preference accepted: %v", value)
		}
	}
	if err := validatePreferences(Preferences{Quality: 1, Cost: 0, Speed: 0.5}); err != nil {
		t.Fatal(err)
	}
}

func TestChildRetainsSelectedParentContext(t *testing.T) {
	parent := Task{ID: "parent", Goal: "Original broad goal", Summary: "Completed first step", RouteScores: map[string]float64{"local": 0.4}, Sources: []Source{{ID: "raw-selected", Kind: "assistant", Text: "Important evidence"}, {ID: "raw-unselected", Kind: "user", Text: "Unrelated history"}}}
	child := Task{Goal: "Distinct current child request", Sources: []Source{}}
	preferences := Preferences{Quality: 1}
	inheritChildContext(&child, parent, Decision{SourceIDs: []string{"raw-selected"}}, RunRequest{Preferences: &preferences})
	if child.Goal != "Distinct current child request" || child.Summary != parent.Summary || child.Preferences != preferences || child.RouteScores["local"] != 0.4 {
		t.Fatalf("child lost structured task state: %+v", child)
	}
	if len(child.Sources) != 2 || child.Sources[0].ID != "task:parent:goal" || child.Sources[0].Text != parent.Goal || child.Sources[1] != parent.Sources[0] {
		t.Fatalf("child lost selected source provenance: %+v", child.Sources)
	}
	child.RouteScores["local"] = -1
	if parent.RouteScores["local"] != 0.4 {
		t.Fatal("child feedback mutated parent's scores")
	}
}

func TestHeterogeneousRouteEffortIsConstrainedAfterSelection(t *testing.T) {
	c := fixtureConfig(t, func(map[string]any) map[string]any { return nil })
	c.Routes[0].Efforts = []string{"low"}
	other := c.Routes[0]
	other.ID = "high-only"
	other.Efforts = []string{"high"}
	c.Routes = append(c.Routes, other)
	calls := 0
	d, err := decideWithTransport(context.Background(), c, Task{Goal: "goal"}, RunRequest{}, func(_ context.Context, _ provider.Provider, _ string, body []byte) ([]byte, error) {
		calls++
		var in struct {
			Questions map[string]struct {
				Criteria map[string]any `json:"criteria"`
			} `json:"questions"`
		}
		if err := json.Unmarshal(body, &in); err != nil {
			t.Fatal(err)
		}
		if calls == 1 {
			if _, ok := in.Questions["effort"]; ok {
				t.Fatal("offered independent effort before choosing heterogeneous route")
			}
			return []byte(`{"answers":{"route":{"choice":"local"},"action":{"choice":"execute"}}}`), nil
		}
		criteria := in.Questions["effort"].Criteria
		if len(criteria) != 1 {
			t.Fatalf("offered incompatible effort: %+v", criteria)
		}
		if _, ok := criteria["low"]; !ok {
			t.Fatal("chosen route's effort missing")
		}
		return []byte(`{"answers":{"effort":{"choice":"low"}}}`), nil
	})
	if err != nil || calls != 2 || d.RouteID != "local" || d.Effort != "low" {
		t.Fatalf("route effort decision: %+v, calls %d, error %v", d, calls, err)
	}
}
func TestEnablingConfigReleasesQueuedTask(t *testing.T) {
	c := fixtureConfig(t, func(map[string]any) map[string]any { return nil })
	c.Enabled = false
	e := recordingExecutor{inputs: make(chan RunInput, 1)}
	m, err := New(t.TempDir(), e)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	if err = m.SetConfig(c); err != nil {
		t.Fatal(err)
	}
	task, err := m.Create(CreateInput{Goal: "queued goal"})
	if err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	m.tasks[task.ID].Status = "queued"
	m.tasks[task.ID].Runs = append(m.tasks[task.ID].Runs, Run{ID: "queued-run", Status: "queued", Request: RunRequest{Message: "queued request"}})
	m.decision = func(context.Context, Config, Task, RunRequest) (Decision, error) {
		return Decision{RouteID: "local", Effort: "low", Action: "execute"}, nil
	}
	m.mu.Unlock()
	c.Enabled = true
	if err = m.SetConfig(c); err != nil {
		t.Fatal(err)
	}
	waitTask(t, m, task.ID, "completed")
}

type functionExecutor func(context.Context, RunInput, func(Event)) (RunResult, error)

func (f functionExecutor) Run(ctx context.Context, in RunInput, emit func(Event)) (RunResult, error) {
	return f(ctx, in, emit)
}

func TestOwnedChildActionAndFinalProvenance(t *testing.T) {
	c := fixtureConfig(t, func(map[string]any) map[string]any {
		return map[string]any{"route": map[string]any{"choice": "local"}, "effort": map[string]any{"choice": "low"}, "action": map[string]any{"choice": "child"}}
	})
	calls := 0
	e := functionExecutor(func(ctx context.Context, in RunInput, emit func(Event)) (RunResult, error) {
		emit(Event{ID: "progress-" + in.RunID, Kind: "progress", Text: "Observed execution started"})
		emit(Event{Kind: "commentary_delta", Text: "Commentary is not the final answer"})
		_, err := in.RequestAction(ctx, ActionRequest{Kind: "input", Questions: []ActionQuestion{{ID: "question", Question: "A secret value", IsSecret: true}}, AllowedDecisions: []string{"cancel"}})
		if err != nil {
			return RunResult{}, err
		}
		emit(Event{Kind: "message_delta", Text: "Actual final child answer"})
		return RunResult{ThreadID: "actual-child-thread", Artifacts: []Artifact{{Path: "child-result.txt"}}}, nil
	})
	dir := t.TempDir()
	m, err := New(dir, e)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	if err = m.SetConfig(c); err != nil {
		t.Fatal(err)
	}
	m.UseDecisionTransport(func(context.Context, provider.Provider, string, []byte) ([]byte, error) {
		calls++
		return []byte(`{"answers":{"route":{"choice":"local"},"effort":{"choice":"low"},"action":{"choice":"child"}}}`), nil
	})
	parent, err := m.Create(CreateInput{Goal: "Overall goal"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.Start(parent.ID, RunRequest{Message: "Distinct current request"}); err != nil {
		t.Fatal(err)
	}
	waiting := waitTask(t, m, parent.ID, "awaiting_action")
	pr := waiting.Runs[0]
	if pr.ChildTaskID == "" || pr.ChildRunID == "" || len(waiting.PendingActions) != 1 {
		t.Fatalf("handoff was not bound: %+v", waiting)
	}
	child, err := m.Task(pr.ChildTaskID)
	if err != nil {
		t.Fatal(err)
	}
	cr := child.Runs[0]
	if cr.ParentTaskID != parent.ID || cr.ParentRunID != pr.ID || cr.Route.ID != pr.Route.ID || cr.Context != pr.Context {
		t.Fatalf("child did not preserve the selected execution: %+v", cr)
	}
	unrelated, err := m.Create(CreateInput{Goal: "unrelated"})
	if err != nil {
		t.Fatal(err)
	}
	actionID := waiting.PendingActions[0].ID
	if err = m.ReplyAction(unrelated.ID, actionID, ActionResponse{Answers: map[string][]string{"question": {"private-answer-never-persist"}}}); err == nil {
		t.Fatal("unrelated task answered owned child")
	}
	if err = m.ReplyAction(parent.ID, actionID, ActionResponse{Answers: map[string][]string{"question": {"private-answer-never-persist"}}}); err != nil {
		t.Fatal(err)
	}
	done := waitTask(t, m, parent.ID, "completed")
	if calls != 1 {
		t.Fatalf("child made another planning decision: %d", calls)
	}
	if done.Runs[0].ThreadID != "actual-child-thread" || done.State.CurrentResult.Text != "Actual final child answer" || done.State.CurrentResult.TaskID != pr.ChildTaskID || done.State.CurrentResult.RunID != pr.ChildRunID {
		t.Fatalf("parent lost actual execution provenance: %+v", done)
	}
	if len(done.Artifacts) != 1 || done.Artifacts[0].TaskID != pr.ChildTaskID || done.Artifacts[0].RunID != pr.ChildRunID {
		t.Fatalf("parent artifact is not an owned reference: %+v", done.Artifacts)
	}
	if err = m.Feedback(parent.ID, FeedbackInput{RunID: pr.ID, Rating: 1}); err != nil {
		t.Fatal(err)
	}
	after, _ := m.Task(parent.ID)
	if after.RouteScores[cr.Route.ID] != 0.2 {
		t.Fatal("parent feedback did not use actual executed route")
	}
	b, err := os.ReadFile(filepath.Join(dir, "events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "private-answer-never-persist") {
		t.Fatal("input answer leaked to raw records")
	}
	b, err = os.ReadFile(filepath.Join(dir, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "private-answer-never-persist") {
		t.Fatal("input answer leaked to state")
	}
	if err = m.ReplyAction(parent.ID, actionID, ActionResponse{Decision: "cancel"}); err == nil {
		t.Fatal("stale action accepted")
	}
}
func TestFullCurrentRequestAndBudget(t *testing.T) {
	message := strings.Repeat("x", 5000) + " FINAL CONSTRAINT: never edit outside this task"
	c := fixtureConfig(t, func(map[string]any) map[string]any { return nil })
	c.ContextBytes = 8000
	seen := false
	task := Task{Goal: "A short goal", Sources: []Source{{ID: "history", Text: strings.Repeat("irrelevant", 4000)}}}
	d, err := decideWithTransport(context.Background(), c, task, RunRequest{Message: message}, func(_ context.Context, _ provider.Provider, _ string, body []byte) ([]byte, error) {
		var in struct {
			State struct {
				Current string `json:"current_request"`
			} `json:"state"`
		}
		if err := json.Unmarshal(body, &in); err != nil {
			t.Fatal(err)
		}
		seen = in.State.Current == message
		return []byte(`{"answers":{"route":{"choice":"local"},"effort":{"choice":"low"},"action":{"choice":"execute"}}}`), nil
	})
	if err != nil || !seen {
		t.Fatalf("Jev lost trailing current constraint: %v", err)
	}
	d.SourceIDs = []string{"history"}
	prompt := contextPrompt(task, RunRequest{Message: message}, d, 8000)
	if !strings.Contains(prompt, message) || len(prompt) > 8000 {
		t.Fatal("optional history displaced the full request")
	}
	m, err := New(t.TempDir(), recordingExecutor{inputs: make(chan RunInput, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	c.ContextBytes = 1024
	if err = m.SetConfig(c); err != nil {
		t.Fatal(err)
	}
	created, err := m.Create(CreateInput{Goal: "small goal"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.Start(created.ID, RunRequest{Message: message}); err == nil || !strings.Contains(err.Error(), "complete current request") {
		t.Fatalf("overbudget request was silently shortened: %v", err)
	}
}
func TestDependencyEvidenceVersionsAndStaleChild(t *testing.T) {
	m, err := New(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	dependency, err := m.Create(CreateInput{Goal: "dependency"})
	if err != nil {
		t.Fatal(err)
	}
	consumer, err := m.Create(CreateInput{Goal: "consumer", Dependencies: []string{dependency.ID}})
	if err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	dep := m.tasks[dependency.ID]
	dep.Status = "completed"
	dep.Runs = []Run{{ID: "first", Status: "completed"}}
	dep.Sources = []Source{{ID: "first-source", Kind: "assistant", Text: "first result", TaskID: dep.ID, RunID: "first"}}
	target := m.tasks[consumer.ID]
	m.refreshDependencySourcesLocked(target)
	firstID := target.Sources[0].ID
	dep.Runs = append(dep.Runs, Run{ID: "second", Status: "completed"})
	dep.Sources = append(dep.Sources, Source{ID: "second-source", Kind: "assistant", Text: "latest result", TaskID: dep.ID, RunID: "second"})
	m.refreshDependencySourcesLocked(target)
	if len(target.Sources) != 1 || target.Sources[0].ID == firstID || target.Sources[0].Text != "latest result" || target.Sources[0].SourceID != "second-source" {
		t.Fatalf("dependency evidence stayed stale: %+v", target.Sources)
	}
	parent := dep
	parent.Runs = []Run{{ID: "old-parent", Status: "completed", ChildTaskID: target.ID, ChildRunID: "old-child"}, {ID: "new-parent", Status: "running", Request: RunRequest{Message: "new current request"}}}
	parent.Status = "running"
	target.Runs = []Run{{ID: "old-child", Status: "completed", ParentTaskID: parent.ID, ParentRunID: "old-parent"}}
	m.refreshStateLocked(parent)
	before := clone(parent.State)
	m.syncParentCompletionLocked(target, &target.Runs[0])
	if parent.Status != "running" || parent.Runs[1].Status != "running" || parent.State.CurrentRequest != before.CurrentRequest {
		t.Fatal("old child completion altered new parent")
	}
	m.mu.Unlock()
}
func TestSandboxAndWorkingDirectoryValidation(t *testing.T) {
	route := Route{ID: "local", Adapter: "codex-app-server", Model: "model", Command: "command", CWD: t.TempDir(), Sandbox: "workspace-write"}
	if err := validateRoute(route); err != nil {
		t.Fatal(err)
	}
	route.Sandbox = "danger-full-access"
	if err := validateRoute(route); err == nil {
		t.Fatal("full access accepted")
	}
	route.Sandbox = "read-only"
	route.CWD = filepath.Join(t.TempDir(), "missing")
	if err := validateRoute(route); err == nil {
		t.Fatal("missing working directory accepted")
	}
}

func TestCancelParentExpiresLinkedChildAction(t *testing.T) {
	c := fixtureConfig(t, func(map[string]any) map[string]any { return nil })
	e := functionExecutor(func(ctx context.Context, in RunInput, emit func(Event)) (RunResult, error) {
		_, err := in.RequestAction(ctx, ActionRequest{Kind: "command", Command: "a concrete command", AllowedDecisions: []string{"decline", "cancel"}})
		return RunResult{}, err
	})
	m, err := New(t.TempDir(), e)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	if err = m.SetConfig(c); err != nil {
		t.Fatal(err)
	}
	m.UseDecisionTransport(func(context.Context, provider.Provider, string, []byte) ([]byte, error) {
		return []byte(`{"answers":{"route":{"choice":"local"},"effort":{"choice":"low"},"action":{"choice":"child"}}}`), nil
	})
	parent, err := m.Create(CreateInput{Goal: "cancel goal"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.Start(parent.ID, RunRequest{}); err != nil {
		t.Fatal(err)
	}
	waiting := waitTask(t, m, parent.ID, "awaiting_action")
	actionID := waiting.PendingActions[0].ID
	childID := waiting.Runs[0].ChildTaskID
	if err = m.Cancel(parent.ID); err != nil {
		t.Fatal(err)
	}
	done := waitTask(t, m, parent.ID, "cancelled")
	child, err := m.Task(childID)
	if err != nil {
		t.Fatal(err)
	}
	if child.Status != "cancelled" || len(child.PendingActions) != 0 || len(done.PendingActions) != 0 {
		t.Fatalf("linked action survived cancellation: %+v %+v", done, child)
	}
	if err = m.ReplyAction(parent.ID, actionID, ActionResponse{Decision: "decline"}); err == nil {
		t.Fatal("expired child approval accepted")
	}
}

func TestSecretAnswersRedactedBeforeDurability(t *testing.T) {
	c := fixtureConfig(t, func(map[string]any) map[string]any { return nil })
	secret := "fixture-token-\"quoted\"\n<&>-never-store"
	e := functionExecutor(func(ctx context.Context, in RunInput, emit func(Event)) (RunResult, error) {
		response, err := in.RequestAction(ctx, ActionRequest{Kind: "input", Questions: []ActionQuestion{{ID: "password", Question: "Password", IsSecret: true}}, AllowedDecisions: []string{"cancel"}})
		if err != nil {
			return RunResult{}, err
		}
		value := response.Answers["password"][0]
		payload, _ := json.Marshal(map[string]string{"echo": value})
		emit(Event{Kind: "executor_event", Text: string(payload)})
		middle := len(value) / 2
		emit(Event{Kind: "message_delta", Text: "Final echo: " + value[:middle]})
		emit(Event{Kind: "message_delta", Text: value[middle:]})
		emit(Event{Kind: "error", Text: "Reported error " + value})
		return RunResult{ThreadID: "thread-" + value, Artifacts: []Artifact{{ID: "artifact-" + value, Path: "result-" + value, SourceID: "source-" + value}}}, errors.New("Executor failure " + value)
	})
	dir := t.TempDir()
	m, err := New(dir, e)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	if err = m.SetConfig(c); err != nil {
		t.Fatal(err)
	}
	m.UseDecisionTransport(func(context.Context, provider.Provider, string, []byte) ([]byte, error) {
		return []byte(`{"answers":{"route":{"choice":"local"},"effort":{"choice":"low"},"action":{"choice":"child"}}}`), nil
	})
	parent, err := m.Create(CreateInput{Goal: "secret test"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.Start(parent.ID, RunRequest{}); err != nil {
		t.Fatal(err)
	}
	waiting := waitTask(t, m, parent.ID, "awaiting_action")
	if err = m.ReplyAction(parent.ID, waiting.PendingActions[0].ID, ActionResponse{Answers: map[string][]string{"password": {secret}}}); err != nil {
		t.Fatal(err)
	}
	done := waitTask(t, m, parent.ID, "failed")
	encoded, _ := json.Marshal(secret)
	escaped := string(encoded[1 : len(encoded)-1])
	assertClean := func(label, text string) {
		t.Helper()
		if strings.Contains(text, secret) || strings.Contains(text, escaped) {
			t.Fatalf("secret persisted in %s", label)
		}
	}
	assertClean("parent error", done.Runs[0].Error)
	assertClean("parent final", done.State.CurrentResult.Text)
	if !strings.Contains(done.State.CurrentResult.Text, "[redacted]") {
		t.Fatal("final output was not redacted")
	}
	assertClean("thread", done.Runs[0].ThreadID)
	for _, a := range done.Artifacts {
		assertClean("artifact metadata", a.ID+a.Path+a.SourceID)
	}
	for _, name := range []string{"state.json", "events.jsonl"} {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		assertClean(name, string(b))
		if name == "events.jsonl" {
			for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
				var event Event
				if err := json.Unmarshal([]byte(line), &event); err != nil {
					t.Fatal(err)
				}
				assertClean("decoded event", event.Text)
			}
		}
	}
	m.mu.Lock()
	retained := len(m.secrets) + len(m.privateOutput)
	m.mu.Unlock()
	if retained != 0 {
		t.Fatal("secret redaction memory survived run completion")
	}
}

func TestSecretProgressFragmentsNeverPersist(t *testing.T) {
	c := fixtureConfig(t, func(map[string]any) map[string]any { return nil })
	e := functionExecutor(func(ctx context.Context, in RunInput, emit func(Event)) (RunResult, error) {
		response, err := in.RequestAction(ctx, ActionRequest{Kind: "input", Questions: []ActionQuestion{{ID: "secret", Question: "Secret", IsSecret: true}}, AllowedDecisions: []string{"cancel"}})
		if err != nil {
			return RunResult{}, err
		}
		value := response.Answers["secret"][0]
		emit(Event{Kind: "progress", Text: value[:3]})
		emit(Event{Kind: "progress", Text: value[3:]})
		emit(Event{Kind: "message_delta", Text: "Completed"})
		return RunResult{}, nil
	})
	dir := t.TempDir()
	m, err := New(dir, e)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	if err = m.SetConfig(c); err != nil {
		t.Fatal(err)
	}
	m.UseDecisionTransport(func(context.Context, provider.Provider, string, []byte) ([]byte, error) {
		return []byte(`{"answers":{"route":{"choice":"local"},"effort":{"choice":"low"},"action":{"choice":"execute"}}}`), nil
	})
	task, err := m.Create(CreateInput{Goal: "progress privacy"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.Start(task.ID, RunRequest{}); err != nil {
		t.Fatal(err)
	}
	waiting := waitTask(t, m, task.ID, "awaiting_action")
	if err = m.ReplyAction(task.ID, waiting.PendingActions[0].ID, ActionResponse{Answers: map[string][]string{"secret": {"abcdef"}}}); err != nil {
		t.Fatal(err)
	}
	done := waitTask(t, m, task.ID, "completed")
	for _, e := range done.Events {
		if e.Kind == "progress" && (strings.Contains(e.Text, "abc") || strings.Contains(e.Text, "def")) {
			t.Fatal("secret progress fragment reached task state")
		}
	}
	b, err := os.ReadFile(filepath.Join(dir, "events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		var e Event
		if err = json.Unmarshal([]byte(line), &e); err != nil {
			t.Fatal(err)
		}
		if e.Kind == "progress" {
			count++
			if strings.Contains(e.Text, "abc") || strings.Contains(e.Text, "def") {
				t.Fatal("secret progress fragment reached decoded journal")
			}
		}
	}
	if count != 2 {
		t.Fatalf("lost progress provenance records: %d", count)
	}
	if done.State.Progress.Text != "[Progress details withheld after secret input]" || done.Status != "completed" {
		t.Fatalf("generic lifecycle/progress unavailable: %+v", done.State)
	}
}
