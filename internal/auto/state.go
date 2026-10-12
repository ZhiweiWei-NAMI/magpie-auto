package auto

import (
	"slices"
	"strings"
)

func findRun(t *Task, id string) *Run {
	if t == nil {
		return nil
	}
	for i := range t.Runs {
		if t.Runs[i].ID == id {
			return &t.Runs[i]
		}
	}
	return nil
}
func latestRun(t *Task) *Run {
	if t == nil || len(t.Runs) == 0 {
		return nil
	}
	return &t.Runs[len(t.Runs)-1]
}
func isActiveStatus(status string) bool {
	return slices.Contains([]string{"queued", "deciding", "running", "waiting", "awaiting_action", "stopping"}, status)
}
func recordForEvent(e Event) StateRecord {
	taskID, runID, sourceID := e.TaskID, e.RunID, e.ID
	if e.OriginTaskID != "" {
		taskID = e.OriginTaskID
	}
	if e.OriginRunID != "" {
		runID = e.OriginRunID
	}
	if e.SourceID != "" {
		sourceID = e.SourceID
	}
	return StateRecord{Text: e.Text, SourceID: sourceID, TaskID: taskID, RunID: runID}
}
func sourceForEvent(e Event) Source {
	r := recordForEvent(e)
	return Source{ID: r.SourceID, SourceID: r.SourceID, Kind: e.Kind, Text: e.Text, TaskID: r.TaskID, RunID: r.RunID}
}

// State is derived only from recorded requests, executor output and owned lifecycle.
func (m *Manager) refreshStateLocked(t *Task) {
	s := CurrentState{Goal: StateRecord{Text: t.Goal, TaskID: t.ID}, Phase: t.Status, CompletedResults: []StateRecord{}, PendingDependencies: []DependencyState{}, SourceRefs: []SourceRef{}}
	run := latestRun(t)
	if run != nil {
		s.CurrentRequest = StateRecord{Text: run.Request.Message, TaskID: t.ID, RunID: run.ID}
		s.ExplicitConstraints = ExplicitConstraints{RouteID: run.Request.RouteID, Effort: run.Request.Effort}
	}
	for _, e := range t.Events {
		if e.Kind == "created" && s.Goal.SourceID == "" {
			s.Goal.SourceID = e.ID
		}
		if run != nil && e.RunID == run.ID {
			if e.Kind == "user" {
				s.CurrentRequest = recordForEvent(e)
			}
			if e.Kind == "progress" {
				s.Progress = recordForEvent(e)
				s.Progress.Text = bounded(s.Progress.Text, 1024)
			}
		}
	}
	if run == nil {
		s.CurrentRequest = s.Goal
	}
	for _, source := range t.Sources {
		taskID := source.TaskID
		if taskID == "" {
			taskID = t.ID
		}
		s.SourceRefs = append(s.SourceRefs, SourceRef{ID: source.ID, SourceID: source.SourceID, Kind: source.Kind, TaskID: taskID, RunID: source.RunID})
		if source.Kind == "assistant" {
			result := StateRecord{Text: bounded(source.Text, 2048), SourceID: source.ID, TaskID: taskID, RunID: source.RunID}
			s.CurrentResult = result
			if owner := m.tasks[taskID]; owner != nil {
				if r := findRun(owner, source.RunID); r != nil && r.Status == "completed" {
					s.CompletedResults = append(s.CompletedResults, result)
				}
			}
		}
	}
	if len(s.SourceRefs) > 24 {
		s.SourceRefs = s.SourceRefs[len(s.SourceRefs)-24:]
	}
	if len(s.CompletedResults) > 8 {
		s.CompletedResults = s.CompletedResults[len(s.CompletedResults)-8:]
	}
	for _, id := range t.Dependencies {
		dep := m.tasks[id]
		entry := DependencyState{TaskID: id, Status: "missing"}
		if dep != nil {
			entry.Status = dep.Status
			if r := latestRun(dep); r != nil {
				entry.RunID = r.ID
			}
			entry.SourceID = dep.State.CurrentResult.SourceID
		}
		if entry.Status != "completed" {
			s.PendingDependencies = append(s.PendingDependencies, entry)
		}
	}
	t.State = s
}
func (m *Manager) refreshDependencySourcesLocked(t *Task) {
	// A source version is tied to the latest completed run, not just the task ID.
	t.Sources = slices.DeleteFunc(t.Sources, func(s Source) bool {
		return s.Kind == "dependency" && slices.ContainsFunc(t.Dependencies, func(id string) bool {
			return s.TaskID == id || strings.HasPrefix(s.ID, "dependency:"+id+":") || s.ID == "task:"+id
		})
	})
	for _, id := range t.Dependencies {
		dep := m.tasks[id]
		if dep == nil || dep.Status != "completed" {
			continue
		}
		r := latestRun(dep)
		if r == nil || r.Status != "completed" {
			continue
		}
		for i := len(dep.Sources) - 1; i >= 0; i-- {
			s := dep.Sources[i]
			if s.Kind == "assistant" && (s.RunID == r.ID && s.TaskID == id || s.RunID == r.ChildRunID && s.TaskID == r.ChildTaskID) {
				t.Sources = append(t.Sources, Source{ID: "dependency:" + id + ":" + r.ID + ":" + s.ID, SourceID: s.ID, Kind: "dependency", Text: s.Text, TaskID: s.TaskID, RunID: s.RunID})
				break
			}
		}
	}
	m.refreshStateLocked(t)
}
