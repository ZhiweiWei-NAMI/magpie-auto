package auto

import (
	"encoding/json"
	"fmt"
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

// Offer whole sources in their original order. Oversized sources remain archived.
func offeredSources(t Task, limit int) []Source {
	left := max(0, limit-len(t.Goal)-2048)
	var out []Source
	for i := len(t.Sources) - 1; i >= 0; i-- {
		source := t.Sources[i]
		if len(source.Text)+128 > left {
			continue
		}
		out = append(out, source)
		left -= len(source.Text) + 128
	}
	slices.Reverse(out)
	return out
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
		if len(source.Text) > left {
			continue
		}
		b.WriteString(header + source.Text)
		ids = append(ids, source.ID)
	}
	b.WriteString(tail)
	return b.String(), ids
}
