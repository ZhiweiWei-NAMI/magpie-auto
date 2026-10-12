// Package auto provides Jev decisions and durable inference history; clients execute tools.
package auto

import (
	"context"
	"encoding/json"
	"time"
)

type Config struct {
	Enabled       bool    `json:"enabled"`
	Decider       string  `json:"decider"`
	JevKeyFile    string  `json:"jevKeyFile,omitempty"`
	Routes        []Route `json:"routes"`
	MaxConcurrent int     `json:"maxConcurrent"`
	ContextBytes  int     `json:"contextBytes"`
}
type Route struct {
	ID          string   `json:"id"`
	Label       string   `json:"label"`
	Adapter     string   `json:"adapter"`
	Model       string   `json:"model"`
	Description string   `json:"description"`
	Efforts     []string `json:"efforts"`
	Workspace   string   `json:"workspace,omitempty"`
	AllowWrites bool     `json:"allowWrites"`
}
type Preferences struct {
	Quality float64 `json:"quality"`
	Cost    float64 `json:"cost"`
	Speed   float64 `json:"speed"`
}

// ChatTransport serves execution-model inference. Jev uses its own typed API.
// Usage comes from the provider; absent counters and cache measurements stay absent.
type ChatTransport func(context.Context, string, string, string, func(string, string)) (ChatResult, error)
type ChatResult struct {
	Text      string
	RequestID string
	Usage     json.RawMessage
	ToolCalls []ToolCall
	Calls     []ChatUsage
}
type ToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}
type ChatUsage struct {
	RequestID string          `json:"requestId,omitempty"`
	Usage     json.RawMessage `json:"usage,omitempty"`
}
type Artifact struct {
	ID       string `json:"id"`
	TaskID   string `json:"taskId"`
	RunID    string `json:"runId"`
	SourceID string `json:"sourceId"`
	Path     string `json:"path"`
	Bytes    int    `json:"bytes"`
	SHA256   string `json:"sha256"`
}
type Event struct {
	ID     string    `json:"id"`
	TaskID string    `json:"taskId"`
	RunID  string    `json:"runId,omitempty"`
	Kind   string    `json:"kind"`
	Text   string    `json:"text"`
	At     time.Time `json:"at"`
}
type Source struct {
	ID     string `json:"id"`
	TaskID string `json:"taskId"`
	RunID  string `json:"runId,omitempty"`
	Kind   string `json:"kind"`
	Status string `json:"status,omitempty"`
	Text   string `json:"text"`
}
type Decision struct {
	RouteID   string   `json:"routeId"`
	Effort    string   `json:"effort"`
	SourceIDs []string `json:"sourceIds"`
	Reason    string   `json:"reason"`
}
type Run struct {
	ID                string          `json:"id"`
	Status            string          `json:"status"`
	Message           string          `json:"message"`
	Request           RunRequest      `json:"request"`
	Route             Route           `json:"route"`
	Decision          *Decision       `json:"decision,omitempty"`
	Context           string          `json:"context,omitempty"`
	ContextSourceIDs  []string        `json:"contextSourceIds"`
	RequestID         string          `json:"requestId,omitempty"`
	Decider           string          `json:"decider"`
	DecisionRequestID string          `json:"decisionRequestId,omitempty"`
	Usage             json.RawMessage `json:"usage,omitempty"`
	DecisionUsage     json.RawMessage `json:"decisionUsage,omitempty"`
	Calls             []ChatUsage     `json:"calls,omitempty"`
	Error             string          `json:"error,omitempty"`
	Started           time.Time       `json:"started"`
	Finished          time.Time       `json:"finished,omitempty"`
}
type Task struct {
	ID           string             `json:"id"`
	Goal         string             `json:"goal"`
	ParentID     string             `json:"parentId,omitempty"`
	Dependencies []string           `json:"dependencies"`
	Status       string             `json:"status"`
	Preferences  Preferences        `json:"preferences"`
	RouteScores  map[string]float64 `json:"routeScores"`
	State        CurrentState       `json:"state"`
	Runs         []Run              `json:"runs"`
	Events       []Event            `json:"events"`
	Sources      []Source           `json:"sources"`
	Artifacts    []Artifact         `json:"artifacts"`
	Created      time.Time          `json:"created"`
	Updated      time.Time          `json:"updated"`
}
type StateRecord struct {
	Text     string `json:"text"`
	Status   string `json:"status,omitempty"`
	SourceID string `json:"sourceId"`
	TaskID   string `json:"taskId"`
	RunID    string `json:"runId,omitempty"`
}
type CurrentState struct {
	Phase               string        `json:"phase"`
	CurrentRequest      StateRecord   `json:"currentRequest"`
	CurrentResult       StateRecord   `json:"currentResult"`
	PendingDependencies []StateRecord `json:"pendingDependencies"`
	SourceRefs          []Source      `json:"sourceRefs"`
}
type CreateInput struct {
	Goal         string      `json:"goal"`
	ParentID     string      `json:"parentId"`
	Dependencies []string    `json:"dependencies"`
	Preferences  Preferences `json:"preferences"`
}
type RunRequest struct {
	Message     string       `json:"message"`
	RouteID     string       `json:"routeId"`
	Effort      string       `json:"effort"`
	Preferences *Preferences `json:"preferences,omitempty"`
}
type FeedbackInput struct {
	RunID       string       `json:"runId"`
	Rating      float64      `json:"rating"`
	Text        string       `json:"text"`
	Preferences *Preferences `json:"preferences,omitempty"`
}
type TaskSummary struct {
	ID      string    `json:"id"`
	Goal    string    `json:"goal"`
	Status  string    `json:"status"`
	Updated time.Time `json:"updated"`
}
type Snapshot struct {
	Config     Config        `json:"config"`
	Tasks      []TaskSummary `json:"tasks"`
	Configured bool          `json:"configured"`
	Error      string        `json:"error,omitempty"`
}
