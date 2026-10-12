package auto

import (
	"context"
	"time"
)

type Config struct {
	Enabled       bool    `json:"enabled"`
	Decider       string  `json:"decider"`
	Routes        []Route `json:"routes"`
	MaxConcurrent int     `json:"maxConcurrent"`
	ContextBytes  int     `json:"contextBytes"`
}
type Route struct {
	ID          string   `json:"id"`
	Adapter     string   `json:"adapter"`
	Model       string   `json:"model"`
	Description string   `json:"description"`
	Efforts     []string `json:"efforts"`
	Command     string   `json:"command"`
	Args        []string `json:"args"`
	CWD         string   `json:"cwd"`
	Sandbox     string   `json:"sandbox,omitempty"`
}
type Preferences struct {
	Quality float64 `json:"quality"`
	Cost    float64 `json:"cost"`
	Speed   float64 `json:"speed"`
}
type Executor interface {
	Run(context.Context, RunInput, func(Event)) (RunResult, error)
}
type RunInput struct {
	TaskID, RunID, Prompt string
	Route                 Route
	Effort                string
	RequestAction         func(context.Context, ActionRequest) (ActionResponse, error)
}
type RunResult struct {
	ThreadID  string
	Artifacts []Artifact
}
type Event struct {
	ID           string    `json:"id"`
	TaskID       string    `json:"taskId"`
	RunID        string    `json:"runId"`
	Kind         string    `json:"kind"`
	Text         string    `json:"text"`
	At           time.Time `json:"at"`
	SourceID     string    `json:"sourceId,omitempty"`
	OriginTaskID string    `json:"originTaskId,omitempty"`
	OriginRunID  string    `json:"originRunId,omitempty"`
}
type Artifact struct {
	TaskID   string `json:"taskId"`
	ID       string `json:"id"`
	Path     string `json:"path"`
	RunID    string `json:"runId"`
	SourceID string `json:"sourceId"`
}
type Source struct {
	SourceID string `json:"sourceId,omitempty"`
	TaskID   string `json:"taskId,omitempty"`
	RunID    string `json:"runId,omitempty"`
	ID       string `json:"id"`
	Kind     string `json:"kind"`
	Text     string `json:"text"`
}
type Decision struct {
	RouteID   string   `json:"routeId"`
	Effort    string   `json:"effort"`
	Action    string   `json:"action"`
	SourceIDs []string `json:"sourceIds"`
	Reason    string   `json:"reason"`
}
type Run struct {
	ChildTaskID      string     `json:"childTaskId,omitempty"`
	ChildRunID       string     `json:"childRunId,omitempty"`
	ParentTaskID     string     `json:"parentTaskId,omitempty"`
	ParentRunID      string     `json:"parentRunId,omitempty"`
	ContextSourceIDs []string   `json:"contextSourceIds"`
	Route            Route      `json:"route"`
	ID               string     `json:"id"`
	Status           string     `json:"status"`
	Message          string     `json:"message"`
	Request          RunRequest `json:"request"`
	Decision         *Decision  `json:"decision,omitempty"`
	ThreadID         string     `json:"threadId,omitempty"`
	Context          string     `json:"context,omitempty"`
	Error            string     `json:"error,omitempty"`
	Started          time.Time  `json:"started"`
	Finished         time.Time  `json:"finished,omitempty"`
}
type Task struct {
	State          CurrentState       `json:"state"`
	PendingActions []ActionRequest    `json:"pendingActions"`
	ID             string             `json:"id"`
	Goal           string             `json:"goal"`
	ParentID       string             `json:"parentId,omitempty"`
	Dependencies   []string           `json:"dependencies"`
	Status         string             `json:"status"`
	Summary        string             `json:"summary"`
	Preferences    Preferences        `json:"preferences"`
	RouteScores    map[string]float64 `json:"routeScores"`
	Runs           []Run              `json:"runs"`
	Events         []Event            `json:"events"`
	Sources        []Source           `json:"sources"`
	Artifacts      []Artifact         `json:"artifacts"`
	Created        time.Time          `json:"created"`
	Updated        time.Time          `json:"updated"`
}

type ActionRequest struct {
	ID               string           `json:"id"`
	TaskID           string           `json:"taskId"`
	RunID            string           `json:"runId"`
	ThreadID         string           `json:"threadId"`
	TurnID           string           `json:"turnId"`
	ItemID           string           `json:"itemId"`
	Kind             string           `json:"kind"`
	Command          string           `json:"command"`
	CWD              string           `json:"cwd"`
	Reason           string           `json:"reason"`
	Paths            []string         `json:"paths"`
	Questions        []ActionQuestion `json:"questions"`
	AllowedDecisions []string         `json:"allowedDecisions"`
}
type ActionQuestion struct {
	ID       string         `json:"id"`
	Header   string         `json:"header"`
	Question string         `json:"question"`
	IsOther  bool           `json:"isOther"`
	IsSecret bool           `json:"isSecret"`
	Options  []ActionOption `json:"options"`
}
type ActionOption struct {
	Label       string `json:"label"`
	Description string `json:"description"`
}
type ActionResponse struct {
	Decision string              `json:"decision"`
	Answers  map[string][]string `json:"answers,omitempty"`
}
type StateRecord struct {
	Text     string `json:"text"`
	SourceID string `json:"sourceId"`
	TaskID   string `json:"taskId"`
	RunID    string `json:"runId"`
}
type ExplicitConstraints struct {
	RouteID string `json:"routeId,omitempty"`
	Effort  string `json:"effort,omitempty"`
}
type DependencyState struct {
	TaskID   string `json:"taskId"`
	RunID    string `json:"runId"`
	Status   string `json:"status"`
	SourceID string `json:"sourceId"`
}
type SourceRef struct {
	SourceID string `json:"sourceId,omitempty"`
	ID       string `json:"id"`
	Kind     string `json:"kind"`
	TaskID   string `json:"taskId"`
	RunID    string `json:"runId"`
}
type CurrentState struct {
	Goal                StateRecord         `json:"goal"`
	CurrentRequest      StateRecord         `json:"currentRequest"`
	Phase               string              `json:"phase"`
	Progress            StateRecord         `json:"progress"`
	ExplicitConstraints ExplicitConstraints `json:"explicitConstraints"`
	CurrentResult       StateRecord         `json:"currentResult"`
	CompletedResults    []StateRecord       `json:"completedResults"`
	PendingDependencies []DependencyState   `json:"pendingDependencies"`
	SourceRefs          []SourceRef         `json:"sourceRefs"`
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
type Snapshot struct {
	Config     Config `json:"config"`
	Tasks      []Task `json:"tasks"`
	Configured bool   `json:"configured"`
	Error      string `json:"error,omitempty"`
}
