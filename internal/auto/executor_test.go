package auto

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// This child speaks the public v2 protocol and performs no network or model
// calls. The parent uses the real process-tree adapter to exercise its pipes.
func TestCodexProtocolChild(t *testing.T) {
	mode := ""
	for i, arg := range os.Args {
		if arg == "--auto-protocol-fixture" && i+1 < len(os.Args) {
			mode = os.Args[i+1]
		}
	}
	if mode == "" {
		return
	}
	enc := json.NewEncoder(os.Stdout)
	send := func(v any) {
		if err := enc.Encode(v); err != nil {
			os.Exit(2)
		}
	}
	currentID := json.RawMessage("1")
	fail := func(s string) {
		send(map[string]any{"id": currentID, "error": map[string]string{"message": s}})
		os.Exit(2)
	}
	s := bufio.NewScanner(os.Stdin)
	initialized := false
	writeMode := slices.Contains([]string{"write", "file", "file-outside", "file-incomplete", "file-moveoutside", "file-grantoutside", "downgrade"}, mode)
	awaitingAction := false
	finish := func() {
		send(map[string]any{"method": "item/started", "params": map[string]any{"threadId": "owned-thread", "turnId": "owned-turn", "item": map[string]string{"id": "message-1", "type": "agentMessage", "phase": "final_answer", "text": ""}}})
		send(map[string]any{"method": "item/agentMessage/delta", "params": map[string]string{"threadId": "owned-thread", "turnId": "owned-turn", "itemId": "message-1", "delta": "fixture output"}})
		send(map[string]any{"method": "item/completed", "params": map[string]any{"threadId": "owned-thread", "turnId": "owned-turn", "item": map[string]string{"id": "message-1", "type": "agentMessage", "phase": "final_answer", "text": "fixture output"}}})
		send(map[string]any{"method": "turn/completed", "params": map[string]any{"threadId": "owned-thread", "turn": map[string]string{"id": "owned-turn", "status": "completed"}}})
	}
	for s.Scan() {
		var f struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params map[string]any  `json:"params"`
		}
		if json.Unmarshal(s.Bytes(), &f) != nil {
			os.Exit(2)
		}
		currentID = f.ID
		response := func(result any) { send(map[string]any{"id": f.ID, "result": result}) }
		notify := func(method string, params any) { send(map[string]any{"method": method, "params": params}) }
		switch f.Method {
		case "initialize":
			response(map[string]string{"userAgent": "fixture"})
		case "initialized":
			initialized = true
		case "thread/start":
			wantSandbox := "read-only"
			if writeMode {
				wantSandbox = "workspace-write"
			}
			if !initialized || f.Params["sandbox"] != wantSandbox || f.Params["approvalPolicy"] != "on-request" || f.Params["approvalsReviewer"] != "user" || f.Params["ephemeral"] != true {
				fail("unsafe or uninitialized thread")
			}
			if writeMode {
				config, _ := f.Params["config"].(map[string]any)
				roots, _ := config["sandbox_workspace_write.writable_roots"].([]any)
				cwd, _ := os.Getwd()
				if len(roots) != 1 || roots[0] != cwd || config["sandbox_workspace_write.network_access"] != false || config["sandbox_workspace_write.exclude_tmpdir_env_var"] != true || config["sandbox_workspace_write.exclude_slash_tmp"] != true {
					fail("bootstrap sandbox escaped configured workspace")
				}
			}
			returnedPolicy := map[string]any{"type": "readOnly", "networkAccess": false}
			if writeMode && mode != "downgrade" {
				cwd, _ := os.Getwd()
				returnedPolicy = map[string]any{"type": "workspaceWrite", "networkAccess": false, "writableRoots": []string{cwd}, "excludeTmpdirEnvVar": true, "excludeSlashTmp": true}
			}
			response(map[string]any{"thread": map[string]any{"id": "owned-thread", "ephemeral": true}, "approvalPolicy": "on-request", "approvalsReviewer": "user", "sandbox": returnedPolicy})
		case "turn/start":
			policy, _ := f.Params["sandboxPolicy"].(map[string]any)
			wantType := "readOnly"
			if writeMode {
				wantType = "workspaceWrite"
			}
			if f.Params["model"] != "fixture-model" || f.Params["effort"] != "low" || policy["type"] != wantType || policy["networkAccess"] != false {
				fail("routing or sandbox lost")
			}
			if writeMode {
				roots, _ := policy["writableRoots"].([]any)
				cwd, _ := os.Getwd()
				if len(roots) != 1 || roots[0] != cwd || policy["excludeTmpdirEnvVar"] != true || policy["excludeSlashTmp"] != true {
					fail("writable roots escaped configured workspace")
				}
			}
			if mode != "early" {
				response(map[string]any{"turn": map[string]string{"id": "owned-turn", "status": "inProgress"}})
			}
			notify("turn/started", map[string]any{"threadId": "owned-thread", "turn": map[string]string{"id": "owned-turn", "status": "inProgress"}})
			if mode == "approval" {
				send(map[string]any{"id": 99, "method": "item/commandExecution/requestApproval", "params": map[string]string{"threadId": "owned-thread", "turnId": "owned-turn", "itemId": "command-1", "command": "fixture command"}})
				continue
			}
			if strings.HasPrefix(mode, "file") {
				path := "result.txt"
				if mode == "file-outside" {
					path = filepath.Join("..", "escaped.txt")
				}
				kind := map[string]string{"type": "add"}
				if mode == "file-moveoutside" {
					kind = map[string]string{"type": "update", "move_path": filepath.Join("..", "escaped.txt")}
				}
				if mode != "file-incomplete" {
					notify("item/started", map[string]any{"threadId": "owned-thread", "turnId": "owned-turn", "item": map[string]any{"id": "file-1", "type": "fileChange", "status": "inProgress", "changes": []any{map[string]any{"path": path, "kind": kind, "diff": "fixture"}}}})
				}
				approval := map[string]string{"threadId": "owned-thread", "turnId": "owned-turn", "itemId": "file-1"}
				if mode == "file-grantoutside" {
					cwd, _ := os.Getwd()
					approval["grantRoot"] = filepath.Dir(cwd)
				}
				send(map[string]any{"id": 99, "method": "item/fileChange/requestApproval", "params": approval})
				awaitingAction = true
				continue
			}
			if slices.Contains([]string{"input", "inputcancel", "awaitcancel", "terminalwait", "resolvedwait"}, mode) {
				send(map[string]any{"id": 99, "method": "item/tool/requestUserInput", "params": map[string]any{"threadId": "owned-thread", "turnId": "owned-turn", "itemId": "input-1", "isBlocking": true, "questions": []any{map[string]any{"id": "question-1", "header": "Choice", "question": "Pick one", "isOther": true, "isSecret": true, "options": []any{map[string]string{"label": "Option A", "description": "fixture choice"}}}}}})
				awaitingAction = true
				if mode == "resolvedwait" {
					notify("serverRequest/resolved", map[string]any{"threadId": "owned-thread", "requestId": 99})
				}
				if mode == "terminalwait" || mode == "resolvedwait" {
					finish()
				}
				continue
			}
			if mode == "decline" {
				send(map[string]any{"id": 99, "method": "item/commandExecution/requestApproval", "params": map[string]string{"threadId": "owned-thread", "turnId": "owned-turn", "itemId": "command-1", "command": "fixture command"}})
				awaitingAction = true
				continue
			}
			if mode == "phases" {
				notify("item/started", map[string]any{"threadId": "owned-thread", "turnId": "owned-turn", "item": map[string]string{"id": "commentary-1", "type": "agentMessage", "phase": "commentary", "text": ""}})
				for i := 0; i < 8; i++ {
					notify("item/agentMessage/delta", map[string]string{"threadId": "owned-thread", "turnId": "owned-turn", "itemId": "commentary-1", "delta": strings.Repeat("progress ", 256)})
				}
				notify("item/completed", map[string]any{"threadId": "owned-thread", "turnId": "owned-turn", "item": map[string]string{"id": "commentary-1", "type": "agentMessage", "phase": "commentary", "text": strings.Repeat("progress ", 2048)}})
			}
			notify("item/started", map[string]any{"threadId": "owned-thread", "turnId": "owned-turn", "item": map[string]string{"id": "message-1", "type": "agentMessage", "phase": "final_answer", "text": ""}})
			notify("item/agentMessage/delta", map[string]string{"threadId": "owned-thread", "turnId": "owned-turn", "itemId": "message-1", "delta": "fixture output"})
			if mode == "cancel" {
				continue
			}
			notify("item/completed", map[string]any{"threadId": "owned-thread", "turnId": "owned-turn", "item": map[string]string{"id": "message-1", "type": "agentMessage", "phase": "final_answer", "text": "fixture output"}})
			status := "completed"
			turn := map[string]any{"id": "owned-turn", "status": status}
			if mode == "failed" {
				turn["status"] = "failed"
				turn["error"] = map[string]string{"message": "fixture upstream failure"}
			}
			notify("turn/completed", map[string]any{"threadId": "owned-thread", "turn": turn})
			if mode == "early" {
				response(map[string]any{"turn": map[string]string{"id": "owned-turn", "status": "completed"}})
			}
		case "turn/interrupt":
			if f.Params["turnId"] != "owned-turn" {
				fail("interrupt targeted wrong turn")
			}
			response(map[string]any{})
			notify("turn/completed", map[string]any{"threadId": "owned-thread", "turn": map[string]string{"id": "owned-turn", "status": "interrupted"}})
		case "":
			if mode == "terminalwait" || mode == "resolvedwait" {
				fail("late action reply after native completion")
			}
			if !awaitingAction {
				os.Exit(0)
			}
			var answer struct {
				Result struct {
					Decision string `json:"decision"`
					Answers  map[string]struct {
						Answers []string `json:"answers"`
					} `json:"answers"`
				} `json:"result"`
			}
			if json.Unmarshal(s.Bytes(), &answer) != nil {
				fail("bad action response")
			}
			if mode == "input" {
				values := answer.Result.Answers["question-1"].Answers
				if len(values) != 1 || values[0] != "ephemeral fixture answer" {
					fail("user input answer lost")
				}
			} else {
				want := "decline"
				if mode == "file" {
					want = "accept"
				}
				if answer.Result.Decision != want {
					fail("unsafe approval response")
				}
				if mode == "file" {
					if err := os.WriteFile("result.txt", []byte("fixture artifact"), 0600); err != nil {
						fail("fixture artifact write failed")
					}
					notify("item/completed", map[string]any{"threadId": "owned-thread", "turnId": "owned-turn", "item": map[string]any{"id": "file-1", "type": "fileChange", "status": "completed", "changes": []any{map[string]any{"path": "result.txt", "kind": map[string]string{"type": "add"}, "diff": "fixture"}}}})
				}
			}
			awaitingAction = false
			finish()
		default:
			fail(fmt.Sprintf("unexpected method %s", f.Method))
		}
	}
	os.Exit(0)
}

func TestCodexExecutorLifecycle(t *testing.T) {
	for _, mode := range []string{"success", "early", "cancel", "approval", "failed"} {
		t.Run(mode, func(t *testing.T) {
			exe, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			var events []Event
			in := RunInput{TaskID: "task", RunID: "run", Prompt: "bounded context", Effort: "low", Route: Route{Command: exe, Args: []string{"-test.run=^TestCodexProtocolChild$", "--", "--auto-protocol-fixture", mode}, CWD: t.TempDir(), Model: "fixture-model"}}
			result, err := (CodexExecutor{}).Run(ctx, in, func(e Event) {
				events = append(events, e)
				if mode == "cancel" && e.Kind == "message_delta" {
					cancel()
				}
			})
			if result.ThreadID != "owned-thread" {
				t.Fatalf("thread not retained: %+v / %v", result, err)
			}
			switch mode {
			case "success", "early":
				if err != nil {
					t.Fatal(err)
				}
			case "cancel":
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("cancel = %v", err)
				}
			case "approval":
				if err == nil || !strings.Contains(err.Error(), "no permission was granted") {
					t.Fatalf("approval = %v", err)
				}
			case "failed":
				if err == nil || !strings.Contains(err.Error(), "fixture upstream failure") {
					t.Fatalf("failure = %v", err)
				}
			}
			messages, stopping := 0, false
			for _, e := range events {
				if e.TaskID != "task" || e.RunID != "run" || e.ID == "" {
					t.Fatalf("event lost provenance: %+v", e)
				}
				if e.Kind == "message_delta" {
					messages++
				}
				if e.Text == "Codex turn interrupted" {
					stopping = true
				}
			}
			if mode != "approval" && messages != 1 {
				t.Fatalf("expected one streamed message, got %d", messages)
			}
			if mode == "cancel" && !stopping {
				t.Fatal("cancel did not wait for terminal acknowledgement")
			}
		})
	}
}

func TestCodexExecutorWorkspaceActionsAndPhases(t *testing.T) {
	for _, mode := range []string{"write", "file", "file-readonly", "file-outside", "file-incomplete", "file-moveoutside", "file-grantoutside", "decline", "input", "inputcancel", "awaitcancel", "terminalwait", "resolvedwait", "phases", "downgrade"} {
		t.Run(mode, func(t *testing.T) {
			exe, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			in := RunInput{TaskID: "task", RunID: "run", Prompt: "bounded context", Effort: "low", Route: Route{Command: exe, Args: []string{"-test.run=^TestCodexProtocolChild$", "--", "--auto-protocol-fixture", mode}, CWD: t.TempDir(), Model: "fixture-model"}}
			if slices.Contains([]string{"write", "file", "file-outside", "file-incomplete", "file-moveoutside", "file-grantoutside", "downgrade"}, mode) {
				in.Route.Sandbox = "workspace-write"
			}
			var actions []ActionRequest
			in.RequestAction = func(actionCtx context.Context, a ActionRequest) (ActionResponse, error) {
				actions = append(actions, a)
				if a.TaskID != "task" || a.RunID != "run" || a.ThreadID != "owned-thread" || a.TurnID != "owned-turn" || a.ID == "" {
					t.Errorf("action provenance lost: %+v", a)
				}
				if mode == "awaitcancel" {
					cancel()
					<-actionCtx.Done()
					return ActionResponse{}, actionCtx.Err()
				}
				if mode == "terminalwait" || mode == "resolvedwait" {
					<-actionCtx.Done()
					return ActionResponse{}, actionCtx.Err()
				}
				if mode == "inputcancel" {
					return ActionResponse{Decision: "cancel"}, nil
				}
				if mode == "input" {
					if a.Kind != "input" || len(a.Questions) != 1 || !a.Questions[0].IsSecret || !a.Questions[0].IsOther || len(a.Questions[0].Options) != 1 {
						t.Errorf("input shape lost: %+v", a)
					}
					return ActionResponse{Answers: map[string][]string{"question-1": {"ephemeral fixture answer"}}}, nil
				}
				if mode == "file" {
					if a.Kind != "file" || len(a.Paths) != 1 || !slices.Contains(a.AllowedDecisions, "accept") {
						t.Errorf("bounded approval unavailable: %+v", a)
					}
					return ActionResponse{Decision: "accept"}, nil
				}
				if slices.Contains(a.AllowedDecisions, "accept") {
					t.Errorf("sandbox escalation was approvable: %+v", a)
				}
				return ActionResponse{Decision: "decline"}, nil
			}
			var events []Event
			result, err := (CodexExecutor{}).Run(ctx, in, func(e Event) { events = append(events, e) })
			if mode == "downgrade" {
				if err == nil || !strings.Contains(err.Error(), "execution was not started") {
					t.Fatalf("sandbox downgrade did not fail closed: %v", err)
				}
				for _, e := range events {
					if strings.Contains(e.Text, "turn/started") || e.Kind == "message_delta" {
						t.Fatal("execution started after sandbox downgrade")
					}
				}
				return
			}
			if mode == "inputcancel" || mode == "awaitcancel" {
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("cancel = %v", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			var final, commentary string
			ack, raw := false, 0
			for _, e := range events {
				if strings.Contains(e.Text, "ephemeral fixture answer") {
					t.Fatal("secret answer persisted in an event")
				}
				if e.Kind == "message_delta" {
					final += e.Text
				}
				if e.Kind == "commentary_delta" {
					commentary += e.Text
				}
				if e.Kind == "executor_event" {
					raw++
				}
				if e.Text == "Codex turn interrupted" {
					ack = true
				}
			}
			if mode == "inputcancel" || mode == "awaitcancel" {
				if !ack {
					t.Fatal("action cancellation skipped terminal acknowledgement")
				}
			} else if final != "fixture output" {
				t.Fatalf("final answer = %q", final)
			}
			if mode == "phases" && (len(commentary) < 16000 || raw < 10) {
				t.Fatal("commentary/raw events were lost")
			}
			if mode == "file" {
				if len(result.Artifacts) != 1 || result.Artifacts[0].TaskID != "task" || result.Artifacts[0].SourceID == "" {
					t.Fatalf("artifact provenance lost: %+v", result.Artifacts)
				}
				if _, err := os.Stat(result.Artifacts[0].Path); err != nil {
					t.Fatal(err)
				}
			}
			if !slices.Contains([]string{"write", "phases"}, mode) && len(actions) != 1 {
				t.Fatalf("expected one action, got %d", len(actions))
			}
		})
	}
}

func TestCodexExecutorRejectsUnknownSandbox(t *testing.T) {
	_, err := (CodexExecutor{}).Run(context.Background(), RunInput{Route: Route{Sandbox: "danger-full-access"}}, nil)
	if err == nil || !strings.Contains(err.Error(), "unsupported Auto sandbox") {
		t.Fatalf("unsafe sandbox = %v", err)
	}
}

func TestExecutorWorkspacePathRejectsSymlinkEscape(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	link := filepath.Join(root, "link")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlink creation unavailable: %v", err)
	}
	if _, ok := executorWorkspacePath(root, filepath.Join(link, "new.txt")); ok {
		t.Fatal("new file through outside symlink was allowed")
	}
}
