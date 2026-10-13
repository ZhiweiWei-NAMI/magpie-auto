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
)

func TestAtomicJevQuestionsPreserveScoresAndConfidence(t *testing.T) {
	key := filepath.Join(t.TempDir(), "key")
	if err := os.WriteFile(key, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			State     string
			Questions map[string]JevQuestion
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if len(body.Questions) != 2 || strings.Contains(body.State, "questions") {
			t.Error("questions were dropped or duplicated into state")
		}
		_, _ = w.Write([]byte(`{"model":"jev-1.13-free","answers":{"importance":{"type":"score","score":1.75,"confidence":0.6,"probabilities":{"0":0.05,"1":0.15,"2":0.8}},"owner":{"type":"choice","choice":"main","confidence":0.8,"probabilities":{"main":0.9,"child":0.1}}},"usage":{"input_tokens":120,"output_tokens":35}}`))
	}))
	defer server.Close()
	request := PlanRequest{Task: "Keep the regression requirement", Questions: map[string]JevQuestion{
		"importance": {Type: "score", Instructions: "Rate the candidate's relevance to the remaining task.", Criteria: []string{"Unrelated", "Useful evidence", "Required to finish"}},
		"owner":      {Type: "choice", Instructions: "Who needs this candidate?", Criteria: map[string]string{"main": "Main implementation", "child": "Independent review"}},
	}}
	result, err := (jevClient{endpoint: server.URL, client: server.Client()}).ask(context.Background(), key, request)
	if err != nil {
		t.Fatal(err)
	}
	var answers map[string]PrimitiveAnswer
	if err := json.Unmarshal(result.Answers, &answers); err != nil {
		t.Fatal(err)
	}
	if *answers["importance"].Score != 1.75 || *answers["owner"].Confidence != 0.8 || result.ElapsedMS == nil {
		t.Fatalf("primitive evidence lost: %+v", result)
	}
}

func TestAtomicScoreCannotTurnMissingConfidenceIntoZero(t *testing.T) {
	q := JevQuestion{Type: "score", Instructions: "Rate relevance", Criteria: []string{"Unrelated", "Required"}}
	for _, raw := range []string{`{"type":"score","score":null}`, `{"type":"score","score":0,"probabilities":{"0":1,"1":0}}`} {
		if validateAnswer(q, json.RawMessage(raw)) == nil {
			t.Fatal("unobserved evidence accepted as a usable score")
		}
	}
}
