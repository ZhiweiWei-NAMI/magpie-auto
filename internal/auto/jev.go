package auto

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"slices"
	"strings"
	"time"
)

const JevFree = "jev-1.13-free"
const jevEndpoint = "https://opencode.ai/inference/v1/systemone"

// ContextUnit is a complete candidate, never a byte prefix of the source.
// Required units carry user instructions and are retained irrespective of scoring.
type ContextUnit struct {
	ID       string `json:"id"`
	Kind     string `json:"kind"`
	Text     string `json:"text"`
	Required bool   `json:"required"`
}

// PlanRequest contains client-owned state; actions describe capabilities, not
// programs for Magpie to execute. The client implements the selected action.
type PlanRequest struct {
	SessionID   string            `json:"sessionId"`
	Task        string            `json:"task"`
	Trigger     string            `json:"trigger"`
	History     []ContextUnit     `json:"history"`
	Actions     map[string]string `json:"actions,omitempty"`
	Observation map[string]any    `json:"observation,omitempty"`
}

type PlanResult struct {
	Model       string          `json:"model,omitempty"`
	Action      string          `json:"action,omitempty"`
	SelectedIDs []string        `json:"selectedIds"`
	Answers     json.RawMessage `json:"answers,omitempty"`
	Usage       json.RawMessage `json:"usage,omitempty"`
	ElapsedMS   *float64        `json:"elapsedMs,omitempty"`
	HTTPStatus  int             `json:"httpStatus,omitempty"`
}

type jevClient struct {
	endpoint string
	client   *http.Client
}

func AskJev(ctx context.Context, keyFile string, request PlanRequest) (PlanResult, error) {
	return (jevClient{endpoint: jevEndpoint, client: &http.Client{Timeout: 45 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}).ask(ctx, keyFile, request)
}

func (j jevClient) ask(ctx context.Context, keyFile string, request PlanRequest) (result PlanResult, err error) {
	if strings.TrimSpace(request.Task) == "" {
		return result, errors.New("Jev needs the complete current task")
	}
	questions := map[string]any{}
	if len(request.Actions) > 0 {
		questions["route"] = map[string]any{"type": "choice", "instructions": "Which offered action best fits the current task, state and available results? Account for the costs and constraints in the offered action descriptions.", "criteria": request.Actions}
	}
	seen := map[string]bool{}
	for i, unit := range request.History {
		if unit.ID == "" || seen[unit.ID] {
			return result, errors.New("context candidates need unique nonempty IDs")
		}
		seen[unit.ID] = true
		if !unit.Required {
			questions[fmt.Sprintf("keep_%d", i)] = map[string]string{"type": "noul", "instructions": "Does retaining the COMPLETE candidate " + unit.ID + " help fulfill the current task, preserve a constraint, avoid repeating work, or use an existing result?"}
		}
	}
	if len(questions) == 0 {
		return result, errors.New("no decision question was requested")
	}
	state, err := json.Marshal(request)
	if err != nil {
		return result, err
	}
	body, err := json.Marshal(map[string]any{"model": JevFree, "state": string(state), "questions": questions})
	if err != nil {
		return result, err
	}
	key, err := os.ReadFile(keyFile)
	if err != nil {
		return result, fmt.Errorf("read Console key file: %w", err)
	}
	secret := strings.TrimSpace(string(key))
	if secret == "" || strings.ContainsAny(secret, "\r\n\t ") {
		return result, errors.New("Console key file must contain a single nonempty key")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, j.endpoint, bytes.NewReader(body))
	if err != nil {
		return result, err
	}
	req.Header.Set("Authorization", "Bearer "+secret)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "magpie-auto/jev")
	started := time.Now()
	defer func() {
		elapsed := float64(time.Since(started).Microseconds()) / 1000
		result.ElapsedMS = &elapsed
	}()
	response, err := j.client.Do(req)
	if err != nil {
		return result, fmt.Errorf("Jev transport: %w", err)
	}
	defer response.Body.Close()
	result.HTTPStatus = response.StatusCode
	raw, err := io.ReadAll(io.LimitReader(response.Body, 16<<20+1))
	if err != nil || len(raw) > 16<<20 {
		return result, errors.New("Jev response could not be read completely")
	}
	if response.StatusCode != http.StatusOK {
		return result, fmt.Errorf("Jev HTTP %d: %s", response.StatusCode, strings.ReplaceAll(string(raw), secret, "[REDACTED]"))
	}
	var reply struct {
		Model   string                     `json:"model"`
		Answers map[string]json.RawMessage `json:"answers"`
		Usage   json.RawMessage            `json:"usage"`
	}
	if err := json.Unmarshal(raw, &reply); err != nil {
		return result, fmt.Errorf("Jev response JSON: %w", err)
	}
	result.Model, result.Usage = reply.Model, reply.Usage
	result.Answers, err = json.Marshal(reply.Answers)
	if err != nil {
		return result, err
	}
	if len(request.Actions) > 0 {
		var answer struct {
			Type   string `json:"type"`
			Choice string `json:"choice"`
		}
		if err := json.Unmarshal(reply.Answers["route"], &answer); err != nil || answer.Type != "choice" {
			return result, errors.New("Jev did not return the requested choice answer")
		}
		if _, ok := request.Actions[answer.Choice]; !ok {
			return result, errors.New("Jev selected an action outside the offered capabilities")
		}
		result.Action = answer.Choice
	}
	result.SelectedIDs = []string{}
	for i, unit := range request.History {
		keep := unit.Required
		if !keep {
			var answer struct {
				Type string   `json:"type"`
				Noul *float64 `json:"noul"`
			}
			if err := json.Unmarshal(reply.Answers[fmt.Sprintf("keep_%d", i)], &answer); err != nil || answer.Type != "noul" || answer.Noul == nil || *answer.Noul < 0 || *answer.Noul > 1 {
				return result, fmt.Errorf("Jev did not return a valid noul for %s", unit.ID)
			}
			// Only clearly irrelevant evidence is omitted; uncertain evidence stays.
			keep = *answer.Noul >= 0.35
		}
		if keep {
			result.SelectedIDs = append(result.SelectedIDs, unit.ID)
		}
	}
	return result, nil
}

func jevDecision(ctx context.Context, c Config, t Task, r RunRequest, sources []Source) (Decision, ChatResult, error) {
	request := PlanRequest{Task: requiredPrompt(t, r), SessionID: t.ID, Trigger: "task", Actions: map[string]string{}}
	type pick struct{ route, effort string }
	choices := map[string]pick{}
	for _, route := range candidates(c, r) {
		efforts := append([]string{""}, route.Efforts...)
		if r.Effort != "" {
			efforts = []string{r.Effort}
		}
		for _, effort := range efforts {
			id := fmt.Sprintf("route_%d", len(choices))
			choices[id] = pick{route.ID, effort}
			request.Actions[id] = route.Label + ": " + route.Description + "; effort=" + effort
		}
	}
	for _, source := range sources {
		request.History = append(request.History, ContextUnit{ID: source.ID, Kind: source.Kind, Text: source.Text})
	}
	result, err := AskJev(ctx, c.JevKeyFile, request)
	chat := ChatResult{Usage: result.Usage}
	if err != nil {
		return Decision{}, chat, err
	}
	p := choices[result.Action]
	return Decision{RouteID: p.route, Effort: p.effort, SourceIDs: slices.Clone(result.SelectedIDs), Reason: "Jev selected the offered route and relevant complete sources"}, chat, nil
}
