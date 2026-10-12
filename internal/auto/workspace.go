package auto

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode/utf8"
)

type workspaceKey struct{}
type WorkspaceTurn struct {
	Messages    []map[string]any
	AllowWrites bool
}

func WorkspaceRequest(ctx context.Context) (WorkspaceTurn, bool) {
	turn, ok := ctx.Value(workspaceKey{}).(WorkspaceTurn)
	return turn, ok
}
func WorkspaceTools(writes bool) []map[string]any {
	var tools []map[string]any
	for _, name := range []string{"list_files", "read_file", "write_file"} {
		if name == "write_file" && !writes {
			continue
		}
		required := []string{"path"}
		if name == "write_file" {
			required = append(required, "content")
		}
		tools = append(tools, map[string]any{"type": "function", "function": map[string]any{"name": name,
			"description": "Operate only on relative paths in the configured workspace. Read/write at most 8192 UTF-8 bytes; read offset is optional; list at most 100 names.",
			"parameters":  map[string]any{"type": "object", "properties": map[string]any{"path": map[string]string{"type": "string"}, "content": map[string]string{"type": "string"}, "offset": map[string]string{"type": "integer"}}, "required": required, "additionalProperties": false}}})
	}
	return tools
}

type toolRecord struct {
	ID       string    `json:"id"`
	Name     string    `json:"name"`
	Path     string    `json:"path,omitempty"`
	Result   string    `json:"result,omitempty"`
	Error    string    `json:"error,omitempty"`
	Artifact *Artifact `json:"artifact,omitempty"`
}

func (m *Manager) workspaceAgent(ctx context.Context, route Route, effort, prompt string, limit int, emit func(string, string)) (ChatResult, error) {
	root, err := os.OpenRoot(route.Workspace)
	if err != nil {
		return ChatResult{}, err
	}
	defer root.Close()
	messages := []map[string]any{{"role": "user", "content": prompt}}
	var last ChatResult
	var calls []ChatUsage
	seen := map[string]bool{}
	for round := 0; round < 8; round++ {
		if err = ctx.Err(); err != nil {
			return last, err
		}
		// Drop complete old tool rounds, preserving the full original request and
		// the newest round. Never cut tool arguments or required task instructions.
		for {
			b, _ := json.Marshal(messages)
			if len(b) <= limit {
				break
			}
			next := 2
			for next < len(messages) && messages[next]["role"] == "tool" {
				next++
			}
			if next >= len(messages) {
				return last, errors.New("workspace tool context exceeds contextBytes")
			}
			messages = append(messages[:1], messages[next:]...)
		}
		turn := WorkspaceTurn{messages, route.AllowWrites}
		last, err = m.transport(context.WithValue(ctx, workspaceKey{}, turn), route.Model, effort, prompt, emit)
		calls = append(calls, ChatUsage{last.RequestID, last.Usage})
		last.Calls = calls
		usage, _ := json.Marshal(calls[len(calls)-1])
		emit("model_usage", string(usage))
		if err != nil {
			return last, err
		}
		if len(last.ToolCalls) == 0 {
			return last, nil
		}
		if len(last.ToolCalls) > 4 || len(seen)+len(last.ToolCalls) > 16 {
			return last, errors.New("workspace tool call limit exceeded")
		}
		messages = append(messages, map[string]any{"role": "assistant", "content": last.Text, "tool_calls": last.ToolCalls})
		for _, call := range last.ToolCalls {
			if err = ctx.Err(); err != nil {
				return last, err
			}
			if call.ID == "" || len(call.ID) > 128 || seen[call.ID] {
				return last, errors.New("invalid or repeated tool call ID")
			}
			seen[call.ID] = true
			b, _ := json.Marshal(call)
			emit("tool_call", string(b))
			record := toolRecord{ID: call.ID, Name: call.Function.Name}
			record.Result, record.Artifact, err = workspaceTool(ctx, root, route.AllowWrites, call, &record.Path)
			if err != nil {
				record.Error = err.Error()
			}
			b, _ = json.Marshal(record)
			emit("tool_result", string(b))
			messages = append(messages, map[string]any{"role": "tool", "tool_call_id": call.ID, "content": string(b)})
		}
	}
	return last, errors.New("workspace agent reached its eight-round limit")
}
func workspaceTool(ctx context.Context, root *os.Root, writes bool, call ToolCall, path *string) (string, *Artifact, error) {
	var in struct {
		Path    string  `json:"path"`
		Content *string `json:"content"`
		Offset  int64   `json:"offset"`
	}
	decoder := json.NewDecoder(strings.NewReader(call.Function.Arguments))
	decoder.DisallowUnknownFields()
	if len(call.Function.Arguments) > 32768 {
		return "", nil, errors.New("tool arguments exceed their byte limit")
	}
	if err := decoder.Decode(&in); err != nil {
		return "", nil, err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return "", nil, errors.New("tool arguments contain trailing data")
	}
	if !filepath.IsLocal(in.Path) || in.Offset < 0 {
		return "", nil, errors.New("tool paths must be relative and contained in the workspace")
	}
	*path = filepath.ToSlash(filepath.Clean(in.Path))
	if err := ctx.Err(); err != nil {
		return "", nil, err
	}
	if call.Function.Name == "write_file" {
		if !writes || in.Content == nil || len(*in.Content) > 8192 || !utf8.ValidString(*in.Content) {
			return "", nil, errors.New("writes require allowWrites and at most 8192 UTF-8 bytes")
		}
		mode := os.FileMode(0600)
		if info, err := root.Lstat(in.Path); err == nil && !info.Mode().IsRegular() {
			return "", nil, errors.New("write target must be a regular file")
		} else if err != nil && !os.IsNotExist(err) {
			return "", nil, err
		} else if err == nil {
			mode = info.Mode().Perm()
		}
		tmp := filepath.Join(filepath.Dir(in.Path), ".magpie-auto-"+newID())
		defer root.Remove(tmp)
		f, err := root.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
		if err != nil {
			return "", nil, err
		}
		_, err = f.WriteString(*in.Content)
		if closeErr := f.Close(); err == nil {
			err = closeErr
		}
		if err == nil {
			err = ctx.Err()
		}
		if err == nil {
			err = root.Rename(tmp, in.Path)
		}
		if err != nil {
			return "", nil, err
		}
		hash := sha256.Sum256([]byte(*in.Content))
		return "File written", &Artifact{ID: newID(), Path: *path, Bytes: len(*in.Content), SHA256: hex.EncodeToString(hash[:])}, nil
	}
	info, err := root.Lstat(in.Path)
	if err != nil {
		return "", nil, err
	}
	if !info.Mode().IsRegular() && !(call.Function.Name == "list_files" && info.IsDir()) {
		return "", nil, errors.New("tool target must be a regular file or directory")
	}
	f, err := root.Open(in.Path)
	if err != nil {
		return "", nil, err
	}
	defer f.Close()
	switch call.Function.Name {
	case "list_files":
		entries, err := f.ReadDir(101)
		if err != nil && err != io.EOF {
			return "", nil, err
		}
		var names []string
		for _, entry := range entries {
			name := entry.Name()
			if entry.IsDir() {
				name += "/"
			}
			names = append(names, name)
		}
		slices.Sort(names)
		b, _ := json.Marshal(names[:min(100, len(names))])
		return fmt.Sprintf("%s (limited=%t)", b, len(names) > 100), nil, nil
	case "read_file":
		b, err := io.ReadAll(io.NewSectionReader(f, in.Offset, 8192))
		if err != nil {
			return "", nil, err
		}
		text := string(b)
		if len(b) == 8192 && in.Offset+8192 < info.Size() && !utf8.ValidString(text) {
			start := len(text) - 1
			for start > 0 && text[start]&0xc0 == 0x80 {
				start--
			}
			if utf8.ValidString(text[:start]) && !utf8.FullRuneInString(text[start:]) {
				text = text[:start]
			}
		}
		if !utf8.ValidString(text) {
			return "", nil, errors.New("read_file supports UTF-8 text")
		}
		return fmt.Sprintf("offset=%d nextOffset=%d\n%s", in.Offset, in.Offset+int64(len(text)), text), nil, nil
	default:
		return "", nil, errors.New("unsupported workspace tool")
	}
}

// Open only an artifact recorded by this task's actual write, in its original
// workspace. A changed file is not served as the recorded artifact.
func (m *Manager) OpenArtifact(taskID, artifactID string) (*os.File, error) {
	t, err := m.Task(taskID)
	if err != nil {
		return nil, err
	}
	for _, artifact := range t.Artifacts {
		if artifact.ID != artifactID {
			continue
		}
		for _, run := range t.Runs {
			if run.ID != artifact.RunID || run.Route.Adapter != "workspace" {
				continue
			}
			f, err := os.OpenInRoot(run.Route.Workspace, artifact.Path)
			if err != nil {
				return nil, err
			}
			b, err := io.ReadAll(io.LimitReader(f, 8193))
			hash := sha256.Sum256(b)
			if err == nil && (len(b) != artifact.Bytes || hex.EncodeToString(hash[:]) != artifact.SHA256) {
				err = errors.New("artifact no longer matches its recorded contents")
			}
			if err != nil {
				f.Close()
				return nil, err
			}
			_, err = f.Seek(0, io.SeekStart)
			if err != nil {
				f.Close()
				return nil, err
			}
			return f, nil
		}
	}
	return nil, errors.New("artifact is not recorded for this task")
}
