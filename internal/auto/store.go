package auto

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"time"
)

type diskState struct {
	Config Config  `json:"config"`
	Tasks  []*Task `json:"tasks"`
}

func newID() string {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b[:])
}
func clone[T any](v T) T {
	b, _ := json.Marshal(v)
	var out T
	_ = json.Unmarshal(b, &out)
	return out
}
func loadState(dir string) (diskState, error) {
	var s diskState
	b, err := os.ReadFile(filepath.Join(dir, "state.json"))
	if os.IsNotExist(err) {
		return s, nil
	}
	if err == nil {
		err = json.Unmarshal(b, &s)
	}
	return s, err
}
func (m *Manager) saveLocked() error {
	state := diskState{Config: m.config}
	for _, id := range m.order {
		t := m.tasks[id]
		m.refreshLocked(t)
		stored := *t
		stored.Events, stored.Sources = nil, nil
		state.Tasks = append(state.Tasks, &stored)
	}
	b, err := json.Marshal(state)
	path := filepath.Join(m.dir, "state.json")
	if err == nil {
		err = os.WriteFile(path+".tmp", b, 0600)
	}
	if err == nil {
		err = os.Rename(path+".tmp", path)
	}
	if err != nil {
		m.diskErr = fmt.Errorf("Auto state could not be saved: %w", err)
	}
	return err
}
func (m *Manager) remember(t *Task, e Event) {
	t.Events = append(t.Events, e)
	t.Updated = e.At
	if e.Kind == "assistant" || e.Kind == "feedback" || e.Kind == "tool_result" {
		source := Source{ID: e.ID, TaskID: e.TaskID, RunID: e.RunID, Kind: e.Kind, Text: e.Text}
		for _, run := range t.Runs {
			if run.ID == e.RunID {
				source.Status = run.Status
			}
		}
		t.Sources = append(t.Sources, source)
	}
}
func (m *Manager) eventLocked(t *Task, e Event) error {
	e.ID, e.TaskID, e.At = newID(), t.ID, time.Now().UTC()
	b, err := json.Marshal(e)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(m.dir, "events.jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err == nil {
		_, err = f.Write(append(b, '\n'))
		if err == nil {
			err = f.Sync()
		}
		closeErr := f.Close()
		if err == nil {
			err = closeErr
		}
	}
	if err != nil {
		m.diskErr = fmt.Errorf("Auto history could not be saved: %w", err)
		return err
	}
	m.remember(t, e)
	return nil
}
func (m *Manager) replay() error {
	f, err := os.Open(filepath.Join(m.dir, "events.jsonl"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	defer f.Close()
	for _, t := range m.tasks {
		t.Events, t.Sources, t.Artifacts = nil, nil, nil
	}
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 4096), 4<<20)
	for scanner.Scan() {
		var e Event
		if err := json.Unmarshal(scanner.Bytes(), &e); err != nil || e.ID == "" {
			return fmt.Errorf("Auto history contains an invalid event")
		}
		if t := m.tasks[e.TaskID]; t != nil {
			m.remember(t, e)
		}
	}
	return scanner.Err()
}
func latest(t *Task) *Run {
	if len(t.Runs) == 0 {
		return nil
	}
	return &t.Runs[len(t.Runs)-1]
}
func activeStatus(status string) bool {
	return slices.Contains([]string{"queued", "deciding", "running", "stopping"}, status)
}
func (m *Manager) refreshLocked(t *Task) {
	s := CurrentState{Phase: t.Status, CurrentRequest: StateRecord{Text: t.Goal, TaskID: t.ID}}
	if run := latest(t); run != nil {
		s.CurrentRequest = StateRecord{Text: run.Message, TaskID: t.ID, RunID: run.ID, Status: run.Status}
		for _, e := range t.Events {
			if e.RunID == run.ID && e.Kind == "user" {
				s.CurrentRequest.SourceID = e.ID
			}
		}
	}
	t.Sources = slices.DeleteFunc(t.Sources, func(s Source) bool { return s.Kind == "dependency" })
	for _, source := range t.Sources {
		if source.Kind == "assistant" {
			s.CurrentResult = StateRecord{Text: source.Text, TaskID: t.ID, RunID: source.RunID, SourceID: source.ID}
			for _, run := range t.Runs {
				if run.ID == source.RunID {
					s.CurrentResult.Status = run.Status
				}
			}
		}
	}
	for _, id := range t.Dependencies {
		dep := m.tasks[id]
		entry := StateRecord{TaskID: id, Status: "missing"}
		if dep != nil {
			entry.Status = dep.Status
			if run := latest(dep); run != nil {
				entry.RunID = run.ID
				if dep.Status == "completed" {
					for _, source := range dep.Sources {
						if source.Kind == "assistant" && source.RunID == run.ID {
							source.Kind = "dependency"
							t.Sources = append(t.Sources, source)
							entry.SourceID = source.ID
							break
						}
					}
				}
			}
		}
		if entry.Status != "completed" {
			s.PendingDependencies = append(s.PendingDependencies, entry)
		}
	}
	for _, source := range t.Sources[max(0, len(t.Sources)-8):] {
		source.Text = ""
		s.SourceRefs = append(s.SourceRefs, source)
	}
	t.State = s
}

// StoredConfig shares the console configuration with the client decision API.
func StoredConfig(dir string) (Config, error) { state, err := loadState(dir); return state.Config, err }
