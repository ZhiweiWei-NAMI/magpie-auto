package auto

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yetone/magpie/internal/testenv"
)

func TestMain(m *testing.M) { testenv.Main(m) }

func TestCompleteHistorySurvivesRestart(t *testing.T) {
	dir := t.TempDir()
	m, err := New(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	task, err := m.Create(CreateInput{Goal: "Preserve the final instructions"})
	if err != nil {
		t.Fatal(err)
	}
	text := strings.Repeat("中文 evidence ", 500) + "FINAL REQUIREMENT: do not replace missing measurements with zero"
	m.mu.Lock()
	for range 20 {
		err = m.eventLocked(m.tasks[task.ID], Event{Kind: "assistant", Text: text})
	}
	m.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if err = m.Close(); err != nil {
		t.Fatal(err)
	}
	m, err = New(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	restored, err := m.Task(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(restored.Sources) != 20 || restored.Sources[19].Text != text || restored.State.CurrentResult.Text != text {
		t.Fatalf("complete history or final requirement was lost: sources=%d, last bytes=%d", len(restored.Sources), len(restored.State.CurrentResult.Text))
	}
	if got := offeredSources(restored, 64000); len(got) == 0 || got[len(got)-1].Text != text {
		t.Fatal("decider did not receive the complete most recent result")
	}
}

func TestTypedJevAndStableSelection(t *testing.T) {
	keyFile := filepath.Join(t.TempDir(), "console.key")
	if err := os.WriteFile(keyFile, []byte("fixture-secret"), 0600); err != nil {
		t.Fatal(err)
	}
	request := PlanRequest{Task: "Keep all explicit instructions", Actions: map[string]string{"single": "One agent", "review": "Independent reviewer"}, History: []ContextUnit{
		{ID: "first", Kind: "user", Text: "Original requirement", Required: true},
		{ID: "second", Kind: "assistant", Text: strings.Repeat("x", 1500) + "important tail"},
		{ID: "third", Kind: "assistant", Text: "Unrelated evidence"},
	}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer fixture-secret" {
			t.Error("wrong authentication")
		}
		var body struct {
			Model     string
			State     string
			Questions map[string]any
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body.Model != JevFree || !strings.Contains(body.State, "important tail") {
			t.Error("wrong model or truncated state")
		}
		if len(body.Questions) != 3 {
			t.Errorf("unexpected questions: %v", body.Questions)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"model": JevFree, "answers": map[string]any{
			"route":  map[string]any{"type": "choice", "choice": "single", "probabilities": map[string]int{"single": 1, "review": 0}},
			"keep_1": map[string]any{"type": "noul", "noul": 0.8}, "keep_2": map[string]any{"type": "noul", "noul": 0.1}},
			"usage": map[string]int{"input_tokens": 55, "output_tokens": 8}})
	}))
	defer server.Close()
	result, err := (jevClient{endpoint: server.URL, client: server.Client()}).ask(context.Background(), keyFile, request)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(result.SelectedIDs, ",") != "first,second" || result.Action != "single" || result.Model != JevFree || len(result.Usage) == 0 {
		t.Fatalf("wrong decision: %+v", result)
	}
}

func TestJevFailuresNeverBecomeZeroOrRetry(t *testing.T) {
	keyFile := filepath.Join(t.TempDir(), "key")
	if err := os.WriteFile(keyFile, []byte("fixture-secret"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, response := range []string{`{"answers":{"keep_0":{"type":"noul","noul":null}}}`, `{"answers":{}}`} {
		count := 0
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { count++; _, _ = w.Write([]byte(response)) }))
		_, err := (jevClient{endpoint: server.URL, client: server.Client()}).ask(context.Background(), keyFile, PlanRequest{Task: "task", History: []ContextUnit{{ID: "a", Text: "real source"}}})
		server.Close()
		if err == nil || count != 1 {
			t.Fatalf("missing answer accepted or retried: %v, %d", err, count)
		}
	}
	count := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count++
		http.Error(w, "invalid api key fixture-secret", 401)
	}))
	defer server.Close()
	_, err := (jevClient{endpoint: server.URL, client: server.Client()}).ask(context.Background(), keyFile, PlanRequest{Task: "task", Actions: map[string]string{"single": "single"}})
	if err == nil || strings.Contains(err.Error(), "fixture-secret") || !strings.Contains(err.Error(), "401") || count != 1 {
		t.Fatalf("authentication failure was hidden, leaked, or retried: %v", err)
	}
}

func TestMagpieRefusesWorkspaceExecutor(t *testing.T) {
	c := normalizeConfig(Config{Routes: []Route{{ID: "file", Adapter: "workspace", Model: "dsh-workbuddy/deepseek-v4.1-flash", Workspace: t.TempDir()}}})
	if err := validateConfig(c); err == nil {
		t.Fatal("Magpie still accepts a file executor")
	}
}
