package auto

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"unicode/utf8"
)

func bounded(s string, n int) string {
	if len(s) <= n {
		return s
	}
	s = s[:max(0, n)]
	for !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return s
}
func requiredPrompt(t Task, r RunRequest) string {
	constraints, _ := json.Marshal(struct {
		RouteID, Effort string
		Preferences     *Preferences
	}{r.RouteID, r.Effort, r.Preferences})
	return "Task goal:\n" + t.Goal + "\nExplicit current constraints:\n" + string(constraints) +
		"\nCurrent request (highest priority; complete):\n" + r.Message
}
func checkBudget(t Task, r RunRequest, n int) error {
	// Reserve the complete goal, request, role and route metadata before history.
	if size := len(requiredPrompt(t, r)) + 512; size > n {
		return fmt.Errorf("complete goal/request need %d bytes, exceeding contextBytes %d; shorten the request or increase the budget", size, n)
	}
	return nil
}
func candidates(c Config, r RunRequest) []Route {
	var out []Route
	for _, route := range c.Routes {
		if (r.RouteID == "" || route.ID == r.RouteID) && (r.Effort == "" || slices.Contains(route.Efforts, r.Effort)) {
			out = append(out, route)
		}
	}
	return out
}

// Recent references are bounded before the decider sees them. Dependency
// sources retain their original task/run/event identity and the latest result.
func offeredSources(t Task, limit int) []Source {
	var out []Source
	left := max(0, limit-len(t.Goal)-2048)
	for i := len(t.Sources) - 1; i >= 0 && len(out) < 8 && left > 128; i-- {
		s := t.Sources[i]
		s.Text = bounded(s.Text, min(1200, left-128))
		out = append(out, s)
		left -= len(s.Text) + 128
	}
	slices.Reverse(out)
	for i := len(t.Sources) - 1; i >= 0; i-- {
		if t.Sources[i].Kind != "assistant" {
			continue
		}
		if !slices.ContainsFunc(out, func(s Source) bool { return s.ID == t.Sources[i].ID }) && len(out) > 0 {
			s := t.Sources[i]
			s.Text = bounded(s.Text, min(1200, left+len(out[0].Text)))
			out[0] = s
		}
		break
	}
	return out
}
func decisionPrompt(c Config, t Task, r RunRequest, sources []Source) (string, error) {
	// A stable goal prefix and chronological source order expose an opportunity
	// for provider caching. This is ordering, never a claim of cache savings.
	payload := struct {
		Goal        string             `json:"goal"`
		Sources     []Source           `json:"sources"`
		Routes      []Route            `json:"routes"`
		Preferences Preferences        `json:"preferences"`
		Scores      map[string]float64 `json:"feedbackRouteScores"`
		Phase       string             `json:"phase"`
	}{t.Goal, sources, candidates(c, r), t.Preferences, t.RouteScores, t.Status}
	prefix, _ := json.Marshal(payload)
	request, _ := json.Marshal(r)
	prompt := string(prefix) + "\nCurrent request (highest priority; complete):\n" + string(request)
	if len(prompt) > c.ContextBytes {
		return "", errors.New("decision context exceeds contextBytes; shorten the goal/request or reduce route metadata")
	}
	return prompt, nil
}
func parseDecision(text string, c Config, r RunRequest, sources []Source) (Decision, error) {
	var d Decision
	decoder := json.NewDecoder(bytes.NewBufferString(text))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&d); err != nil {
		return d, fmt.Errorf("GLM decision must be a JSON object: %w", err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return d, errors.New("GLM returned content after its JSON decision")
	}
	idx := slices.IndexFunc(candidates(c, r), func(route Route) bool { return route.ID == d.RouteID })
	if idx < 0 {
		return d, errors.New("GLM selected an unconfigured or explicitly excluded route")
	}
	chosen := candidates(c, r)[idx]
	if (d.Effort != "" && !slices.Contains(chosen.Efforts, d.Effort)) || (r.Effort != "" && d.Effort != r.Effort) {
		return d, errors.New("GLM selected an unsupported or explicitly excluded effort")
	}
	seen := map[string]bool{}
	for _, id := range d.SourceIDs {
		if seen[id] || !slices.ContainsFunc(sources, func(s Source) bool { return s.ID == id }) {
			return d, errors.New("GLM selected an unknown or repeated context source")
		}
		seen[id] = true
	}
	if len(d.Reason) > 1000 || strings.TrimSpace(d.Reason) == "" {
		return d, errors.New("GLM must give a short decision reason")
	}
	return d, nil
}
func executionPrompt(t Task, r RunRequest, route Route, d Decision, sources []Source, limit int) (string, []string) {
	var b strings.Builder
	b.WriteString("Configured agent role: " + route.Label + "\n" + route.Description + "\nTask goal:\n" + t.Goal + "\n")
	constraints, _ := json.Marshal(struct{ RouteID, Effort string }{d.RouteID, d.Effort})
	tail := "\nExplicit constraints: " + string(constraints) + "\nCurrent request (highest priority; complete):\n" + r.Message
	var ids []string
	for _, source := range sources {
		if !slices.Contains(d.SourceIDs, source.ID) {
			continue
		}
		header := fmt.Sprintf("\nReference [%s kind=%s task=%s run=%s]:\n", source.ID, source.Kind, source.TaskID, source.RunID)
		left := limit - b.Len() - len(tail) - len(header)
		if left <= 0 {
			continue
		}
		b.WriteString(header + bounded(source.Text, left))
		ids = append(ids, source.ID)
	}
	b.WriteString(tail)
	return b.String(), ids
}
