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
func (m *Manager) saveLocked() error {
	state := diskState{Config: m.config}
	for _, id := range m.order {
		state.Tasks = append(state.Tasks, m.tasks[id])
	}
	b, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(m.dir, "state.json")
	if err = os.WriteFile(path+".tmp", b, 0600); err != nil {
		return err
	}
	return os.Rename(path+".tmp", path)
}
func (m *Manager) eventLocked(t *Task, e Event) error {
	e.Text = m.redactRunTextLocked(e.RunID, e.Text)
	if e.OriginRunID != "" {
		e.Text = m.redactRunTextLocked(e.OriginRunID, e.Text)
	}
	if e.ID == "" {
		e.ID = newID()
	}
	e.TaskID = t.ID
	if e.At.IsZero() {
		e.At = time.Now().UTC()
	}
	b, err := json.Marshal(e)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(m.dir, "events.jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	_, err = f.Write(append(b, '\n'))
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	t.Events = append(t.Events, e)
	t.Updated = e.At
	if e.Kind == "user" || e.Kind == "assistant" || e.Kind == "feedback" {
		source := sourceForEvent(e)
		if !slices.ContainsFunc(t.Sources, func(s Source) bool { return s.ID == source.ID }) {
			t.Sources = append(t.Sources, source)
		}
	}
	m.refreshStateLocked(t)
	return m.saveLocked()
}
func clone[T any](v T) T { b, _ := json.Marshal(v); var out T; _ = json.Unmarshal(b, &out); return out }
func loadState(dir string) (diskState, error) {
	var s diskState
	b, err := os.ReadFile(filepath.Join(dir, "state.json"))
	if os.IsNotExist(err) {
		return s, nil
	}
	if err != nil {
		return s, err
	}
	if err = json.Unmarshal(b, &s); err != nil {
		return s, fmt.Errorf("Auto state is unreadable: %w", err)
	}
	return s, nil
}

// Replay records that reached the append log before the state snapshot did.
func (m *Manager) replayLocked() error {
	f, err := os.Open(filepath.Join(m.dir, "events.jsonl"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	defer f.Close()
	seen := map[string]bool{}
	for _, t := range m.tasks {
		for _, e := range t.Events {
			seen[e.ID] = true
		}
	}
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 4096), 4<<20)
	for scanner.Scan() {
		var e Event
		if err := json.Unmarshal(scanner.Bytes(), &e); err != nil {
			return fmt.Errorf("Auto raw event log is unreadable: %w", err)
		}
		if seen[e.ID] {
			continue
		}
		t := m.tasks[e.TaskID]
		if t == nil {
			continue
		}
		t.Events = append(t.Events, e)
		t.Updated = e.At
		if e.Kind == "user" || e.Kind == "assistant" || e.Kind == "feedback" {
			source := sourceForEvent(e)
			if !slices.ContainsFunc(t.Sources, func(s Source) bool { return s.ID == source.ID }) {
				t.Sources = append(t.Sources, source)
			}
		}
		seen[e.ID] = true
	}
	return scanner.Err()
}
