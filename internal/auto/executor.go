package auto

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/yetone/magpie/internal/proc"
)

// CodexExecutor owns an app-server process and a fresh thread for each run.
// Continuation is supplied as bounded context by the task manager, not by
// replaying an external CLI's history. Routes default to read-only. An explicit
// workspace-write route permits sandboxed writes in its configured directory;
// approvals cannot silently expand it.
type CodexExecutor struct{}

type executorFrame struct {
	ID     json.RawMessage `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

type executorRead struct {
	frame executorFrame
	err   error
}

type executorWrite struct {
	value any
	done  chan error
}

type executorItem struct {
	ID      string `json:"id"`
	Type    string `json:"type"`
	Text    string `json:"text"`
	Phase   string `json:"phase"`
	Status  string `json:"status"`
	Changes []struct {
		Path string `json:"path"`
		Kind struct {
			Type     string `json:"type"`
			MovePath string `json:"move_path"`
		} `json:"kind"`
	} `json:"changes"`
}

// executorWorkspacePath bounds paths including existing symlinks and the
// existing ancestors of new files. A dangling link is not a new plain file.
func executorWorkspacePath(cwd, path string) (string, bool) {
	if path == "" || !filepath.IsAbs(cwd) {
		return "", false
	}
	root, err := filepath.EvalSymlinks(cwd)
	if err != nil {
		return "", false
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(root, path)
	}
	path = filepath.Clean(path)
	inside := func(p string) bool {
		rel, err := filepath.Rel(root, p)
		return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
	}
	if !inside(path) {
		return "", false
	}
	ancestor := path
	var missing []string
	for {
		_, statErr := os.Lstat(ancestor)
		if statErr == nil {
			resolved, err := filepath.EvalSymlinks(ancestor)
			if err != nil {
				return "", false
			}
			for i := len(missing) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, missing[i])
			}
			return path, inside(resolved)
		}
		if !os.IsNotExist(statErr) {
			return "", false
		}
		parent := filepath.Dir(ancestor)
		if parent == ancestor {
			return "", false
		}
		missing = append(missing, filepath.Base(ancestor))
		ancestor = parent
	}
}

func executorChangePaths(cwd string, item executorItem) ([]string, bool) {
	if item.Type != "fileChange" || len(item.Changes) == 0 {
		return nil, false
	}
	var paths []string
	for _, change := range item.Changes {
		if !slices.Contains([]string{"add", "delete", "update"}, change.Kind.Type) {
			return nil, false
		}
		path, ok := executorWorkspacePath(cwd, change.Path)
		if !ok {
			return nil, false
		}
		paths = append(paths, path)
		if change.Kind.MovePath != "" {
			moved, ok := executorWorkspacePath(cwd, change.Kind.MovePath)
			if !ok {
				return nil, false
			}
			paths = append(paths, moved)
		}
	}
	return paths, true
}

// Run speaks the stable newline-delimited app-server protocol. It never
// enables experimental process APIs, session approvals or permission amendments.
func (CodexExecutor) Run(ctx context.Context, in RunInput, emit func(Event)) (result RunResult, err error) {
	if err := ctx.Err(); err != nil {
		return result, err
	}
	ctx, cancelRun := context.WithCancel(ctx)
	defer cancelRun()
	sandbox := in.Route.Sandbox
	if sandbox == "" {
		sandbox = "read-only"
	}
	if sandbox != "read-only" && sandbox != "workspace-write" {
		return result, fmt.Errorf("unsupported Auto sandbox %q", sandbox)
	}
	policy := map[string]any{"type": "readOnly", "networkAccess": false}
	if sandbox == "workspace-write" {
		info, err := os.Stat(in.Route.CWD)
		if err != nil || !info.IsDir() || !filepath.IsAbs(in.Route.CWD) {
			return result, errors.New("workspace-write requires an existing absolute route directory")
		}
		if filepath.Clean(in.Route.CWD) == filepath.VolumeName(in.Route.CWD)+string(filepath.Separator) {
			return result, errors.New("workspace-write cannot use a filesystem root")
		}
		policy["type"], policy["writableRoots"] = "workspaceWrite", []string{in.Route.CWD}
		policy["excludeTmpdirEnvVar"], policy["excludeSlashTmp"] = true, true
	}
	command := in.Route.Command
	if command == "" {
		command = proc.FindTool("codex")
		if command == "" {
			return result, errors.New("Codex app-server is not installed; configure an executable route command")
		}
	}
	args := in.Route.Args
	if len(args) == 0 {
		args = []string{"app-server"}
	}
	cmd := proc.Command(command, args...)
	cmd.Dir = in.Route.CWD
	cmd.Stderr = io.Discard // Do not persist diagnostic output that may contain credentials.
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return result, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return result, err
	}
	tree, err := proc.StartTree(cmd)
	if err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		return result, fmt.Errorf("start Codex app-server: %w", err)
	}
	readDone := make(chan struct{})
	frames := make(chan executorRead, 16)
	stopRead := make(chan struct{})
	stopWrite := make(chan struct{})
	writeDone := make(chan struct{})
	writes := make(chan executorWrite)
	go func() {
		defer close(writeDone)
		encoder := json.NewEncoder(stdin)
		for {
			select {
			case <-stopWrite:
				return
			case w := <-writes:
				w.done <- encoder.Encode(w.value)
			}
		}
	}()
	send := func(writeCtx context.Context, value any) error {
		w := executorWrite{value: value, done: make(chan error, 1)}
		select {
		case writes <- w:
		case <-writeCtx.Done():
			return writeCtx.Err()
		}
		select {
		case err := <-w.done:
			return err
		case <-writeCtx.Done():
			return writeCtx.Err()
		}
	}
	go func() {
		defer close(readDone)
		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 4096), 2*1024*1024)
		for scanner.Scan() {
			var f executorFrame
			decodeErr := json.Unmarshal(scanner.Bytes(), &f)
			select {
			case frames <- executorRead{f, decodeErr}:
			case <-stopRead:
				return
			}
			if decodeErr != nil {
				return
			}
		}
		readErr := scanner.Err()
		if readErr == nil {
			readErr = io.EOF
		}
		select {
		case frames <- executorRead{err: readErr}:
		case <-stopRead:
		}
	}()
	defer func() {
		close(stopRead)
		close(stopWrite)
		_ = stdin.Close()
		tree.Kill()
		_ = stdout.Close()
		_ = tree.Wait()
		<-readDone
		<-writeDone
	}()
	nextID, sequence := 0, 0
	write := func(writeCtx context.Context, method string, params any) (string, error) {
		nextID++
		id := fmt.Sprint(nextID)
		return id, send(writeCtx, map[string]any{"id": nextID, "method": method, "params": params})
	}
	sourceID := ""
	record := func(kind, text string) string {
		sequence++
		id := fmt.Sprintf("%s-executor-%d", in.RunID, sequence)
		if emit != nil {
			emit(Event{ID: id, TaskID: in.TaskID, RunID: in.RunID, SourceID: sourceID, Kind: kind, Text: text, At: time.Now().UTC()})
		}
		return id
	}
	turnID := ""
	var blocked error
	terminalSeen := false
	var terminalError error
	streamed := map[string]string{}
	phases := map[string]string{}
	buffered := map[string]string{}
	fileChanges := map[string]executorItem{}
	artifacts := map[string]bool{}
	var deferred []executorRead
	queuedFrames := func() <-chan executorRead {
		if len(deferred) == 0 {
			return frames
		}
		ready := make(chan executorRead, 1)
		ready <- deferred[0]
		deferred = deferred[1:]
		return ready
	}
	var handle func(executorFrame) (bool, error)
	handle = func(f executorFrame) (bool, error) {
		sourceID = ""
		if f.Method == "" {
			return false, nil
		}
		var p struct {
			ThreadID  string           `json:"threadId"`
			TurnID    string           `json:"turnId"`
			ItemID    string           `json:"itemId"`
			Delta     string           `json:"delta"`
			Command   string           `json:"command"`
			CWD       string           `json:"cwd"`
			Reason    string           `json:"reason"`
			GrantRoot string           `json:"grantRoot"`
			Questions []ActionQuestion `json:"questions"`
			Turn      struct {
				ID     string `json:"id"`
				Status string `json:"status"`
				Error  *struct {
					Message string `json:"message"`
				} `json:"error"`
			} `json:"turn"`
			Item  executorItem `json:"item"`
			Error *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal(f.Params, &p); err != nil {
			return false, fmt.Errorf("invalid %s notification: %w", f.Method, err)
		}
		if p.ThreadID != "" && result.ThreadID != "" && p.ThreadID != result.ThreadID {
			if len(f.ID) != 0 && string(f.ID) != "null" {
				return false, errors.New("Codex action belongs to another thread")
			}
			return false, nil
		}
		if p.TurnID != "" && turnID != "" && p.TurnID != turnID {
			if len(f.ID) != 0 && string(f.ID) != "null" {
				return false, errors.New("Codex action belongs to another turn")
			}
			return false, nil
		}
		// Preserve the source records behind displayed progress and artifacts.
		// Account/config/auth messages are deliberately outside this allowlist.
		if strings.HasPrefix(f.Method, "item/") || strings.HasPrefix(f.Method, "turn/") || strings.HasPrefix(f.Method, "thread/") || strings.HasPrefix(f.Method, "serverRequest/") || f.Method == "error" {
			sourceID = record("executor_event", f.Method+" "+string(f.Params))
		}
		if len(f.ID) != 0 && string(f.ID) != "null" {
			response := map[string]any{"id": f.ID}
			action := ActionRequest{TaskID: in.TaskID, RunID: in.RunID, ThreadID: p.ThreadID, TurnID: p.TurnID, ItemID: p.ItemID, CWD: p.CWD, Reason: p.Reason, AllowedDecisions: []string{"decline", "cancel"}}
			if action.CWD == "" {
				action.CWD = in.Route.CWD
			}
			switch f.Method {
			case "item/commandExecution/requestApproval":
				action.Kind, action.Command = "command", p.Command
				action.Reason += " This command requests approval beyond the configured sandbox; Auto supports declining it or stopping the turn."
			case "item/fileChange/requestApproval":
				action.Kind = "file"
				paths, complete := executorChangePaths(in.Route.CWD, fileChanges[p.ItemID])
				action.Paths = paths
				grantOK := p.GrantRoot == ""
				if !grantOK {
					_, grantOK = executorWorkspacePath(in.Route.CWD, p.GrantRoot)
				}
				if sandbox == "workspace-write" && complete && grantOK {
					action.AllowedDecisions = []string{"accept", "decline", "cancel"}
				} else {
					action.Reason += " Writes outside the configured workspace or incomplete file changes cannot be approved."
				}
			case "item/tool/requestUserInput":
				action.Kind, action.Questions, action.AllowedDecisions = "input", p.Questions, []string{"cancel"}
			default:
				blocked = fmt.Errorf("Codex requires unsupported interactive action: %s; no permission was granted", f.Method)
				record("blocked", blocked.Error())
				switch f.Method {
				case "item/permissions/requestApproval":
					response["result"] = map[string]any{"permissions": map[string]any{}, "scope": "turn"}
				case "mcpServer/elicitation/request":
					response["result"] = map[string]any{"action": "decline", "content": nil}
				default:
					response["error"] = map[string]any{"code": -32601, "message": "Magpie Auto does not support this interactive request"}
				}
				if err := send(ctx, response); err != nil {
					return false, err
				}
				return false, blocked
			}
			if p.ThreadID == "" || p.ThreadID != result.ThreadID || p.TurnID == "" || p.ItemID == "" {
				return false, errors.New("Codex action has incomplete session identifiers")
			}
			if turnID == "" {
				turnID = p.TurnID
			}
			action.ID = record("progress", "Awaiting user action: "+action.Kind)
			if in.RequestAction == nil {
				blocked = fmt.Errorf("Codex requires interactive action: %s; no permission was granted", f.Method)
				record("blocked", blocked.Error())
				if action.Kind != "input" {
					response["result"] = map[string]string{"decision": "decline"}
					if err := send(ctx, response); err != nil {
						return false, err
					}
				}
				return false, blocked
			}
			actionSource := sourceID
			actionCtx, actionCancel := context.WithCancel(ctx)
			type actionResult struct {
				answer ActionResponse
				err    error
			}
			answered := make(chan actionResult, 1)
			go func() { answer, err := in.RequestAction(actionCtx, action); answered <- actionResult{answer, err} }()
			var answer ActionResponse
			var err error
		waitAction:
			for {
				select {
				case <-ctx.Done():
					actionCancel()
					<-answered
					return false, nil // The main loop interrupts the owned turn.
				case a := <-answered:
					answer, err = a.answer, a.err
					break waitAction
				case r := <-frames:
					if r.err != nil {
						actionCancel()
						<-answered
						return false, r.err
					}
					if r.frame.Method == "" || (len(r.frame.ID) != 0 && string(r.frame.ID) != "null") {
						if len(deferred) >= 64 {
							actionCancel()
							<-answered
							return false, errors.New("too many pending Codex protocol requests")
						}
						deferred = append(deferred, r)
						continue
					}
					terminal, notifyErr := handle(r.frame)
					if terminal || notifyErr != nil {
						actionCancel()
						<-answered
						return terminal, notifyErr
					}
					if r.frame.Method == "serverRequest/resolved" {
						var resolved struct {
							RequestID json.RawMessage `json:"requestId"`
						}
						if json.Unmarshal(r.frame.Params, &resolved) == nil && string(resolved.RequestID) == string(f.ID) {
							actionCancel()
							<-answered
							return false, nil // Native request already expired; never write a late answer.
						}
					}
				}
			}
			actionCancel()
			sourceID = actionSource
			if err != nil {
				if ctx.Err() != nil {
					return false, nil
				} // The main loop sends interrupt and awaits acknowledgement.
				return false, fmt.Errorf("Codex action could not be answered: %w", err)
			}
			if action.Kind == "input" {
				if answer.Decision == "cancel" {
					cancelRun()
					return false, nil
				}
				if len(action.Questions) == 0 || len(answer.Answers) != len(action.Questions) {
					return false, errors.New("Codex input requires an answer for each question")
				}
				wireAnswers := map[string]any{}
				for _, q := range action.Questions {
					values := answer.Answers[q.ID]
					if q.ID == "" || len(values) == 0 {
						return false, errors.New("Codex input contains a missing question answer")
					}
					wireAnswers[q.ID] = map[string]any{"answers": values}
				}
				response["result"] = map[string]any{"answers": wireAnswers}
			} else {
				if !slices.Contains(action.AllowedDecisions, answer.Decision) {
					return false, errors.New("Codex approval exceeds the configured route permissions")
				}
				response["result"] = map[string]string{"decision": answer.Decision}
			}
			if err := send(ctx, response); err != nil {
				if ctx.Err() != nil {
					return false, nil
				}
				return false, err
			}
			record("progress", "User action answered: "+action.Kind) // Answers, particularly secrets, stay ephemeral.
			if answer.Decision == "cancel" {
				cancelRun()
			}
			return false, nil
		}
		switch f.Method {
		case "turn/started":
			turnID = p.Turn.ID
			record("progress", "Codex turn started")
		case "item/agentMessage/delta":
			phase := phases[p.ItemID]
			if phase == "" {
				buffered[p.ItemID] += p.Delta
			} else {
				kind := "commentary_delta"
				if phase == "final_answer" {
					kind = "message_delta"
				}
				streamed[p.ItemID] = kind
				record(kind, p.Delta)
			}
		case "item/commandExecution/outputDelta", "item/plan/delta":
			record("progress", p.Delta)
		case "item/started":
			if p.Item.Type == "agentMessage" {
				phases[p.Item.ID] = p.Item.Phase
			}
			if p.Item.Type == "fileChange" {
				fileChanges[p.Item.ID] = p.Item
			}
			if p.Item.Type != "agentMessage" && p.Item.Type != "userMessage" {
				record("progress", p.Item.Type+" started")
			}
		case "item/completed":
			if p.Item.Type == "agentMessage" {
				phase := p.Item.Phase
				if phase == "" {
					phase = phases[p.Item.ID]
				}
				kind := "commentary_delta"
				if phase == "" || phase == "final_answer" {
					kind = "message_delta"
				}
				if streamed[p.Item.ID] != kind {
					text := p.Item.Text
					if text == "" {
						text = buffered[p.Item.ID]
					}
					record(kind, text)
				}
				delete(buffered, p.Item.ID)
			}
			if p.Item.Type == "fileChange" && p.Item.Status == "completed" {
				paths, _ := executorChangePaths(in.Route.CWD, p.Item)
				for _, path := range paths {
					info, err := os.Stat(path)
					if err != nil || !info.Mode().IsRegular() {
						continue
					}
					if artifacts[path] {
						continue
					}
					artifacts[path] = true
					source := record("artifact", path)
					result.Artifacts = append(result.Artifacts, Artifact{ID: source, TaskID: in.TaskID, Path: path, RunID: in.RunID, SourceID: sourceID})
				}
			}
		case "error":
			if p.Error != nil {
				record("error", p.Error.Message)
			}
		case "turn/completed":
			if turnID != "" && p.Turn.ID != turnID {
				return false, nil
			}
			record("progress", "Codex turn "+p.Turn.Status)
			if blocked != nil {
				return true, blocked
			}
			switch p.Turn.Status {
			case "completed":
				return true, nil
			case "interrupted":
				return true, context.Canceled
			case "failed":
				if p.Turn.Error != nil {
					return true, errors.New(p.Turn.Error.Message)
				}
				return true, errors.New("Codex turn failed")
			default:
				return true, fmt.Errorf("unknown Codex terminal status %q", p.Turn.Status)
			}
		}
		return false, nil
	}
	request := func(method string, params any, output any) error {
		requestCtx, requestCancel := context.WithTimeout(ctx, 30*time.Second)
		defer requestCancel()
		id, err := write(requestCtx, method, params)
		if err != nil {
			return err
		}
		for {
			incoming := queuedFrames()
			select {
			case <-requestCtx.Done():
				if ctx.Err() != nil {
					return ctx.Err()
				}
				return fmt.Errorf("Codex %s timed out", method)
			case r := <-incoming:
				if r.err != nil {
					return fmt.Errorf("Codex protocol: %w", r.err)
				}
				f := r.frame
				if f.Method == "" && string(f.ID) == id {
					if f.Error != nil {
						return fmt.Errorf("Codex %s: %s", method, f.Error.Message)
					}
					if output != nil {
						return json.Unmarshal(f.Result, output)
					}
					return nil
				}
				terminal, handleErr := handle(f)
				if terminal {
					terminalSeen, terminalError = true, handleErr
				} else if handleErr != nil {
					return handleErr
				}
			}
		}
	}
	if err := request("initialize", map[string]any{"clientInfo": map[string]string{"name": "magpie_auto", "title": "Magpie Auto", "version": "0.1.0"}}, nil); err != nil {
		return result, err
	}
	if err := send(ctx, map[string]any{"method": "initialized", "params": map[string]any{}}); err != nil {
		return result, err
	}
	var started struct {
		Thread struct {
			ID        string `json:"id"`
			Ephemeral bool   `json:"ephemeral"`
		} `json:"thread"`
		ApprovalPolicy    string `json:"approvalPolicy"`
		ApprovalsReviewer string `json:"approvalsReviewer"`
		Sandbox           struct {
			Type                string   `json:"type"`
			NetworkAccess       *bool    `json:"networkAccess"`
			WritableRoots       []string `json:"writableRoots"`
			ExcludeTmpdirEnvVar bool     `json:"excludeTmpdirEnvVar"`
			ExcludeSlashTmp     bool     `json:"excludeSlashTmp"`
		} `json:"sandbox"`
	}
	threadParams := map[string]any{"model": in.Route.Model, "cwd": in.Route.CWD, "sandbox": sandbox, "approvalPolicy": "on-request", "approvalsReviewer": "user", "ephemeral": true}
	if sandbox == "workspace-write" {
		threadParams["config"] = map[string]any{
			"sandbox_workspace_write.writable_roots":         []string{in.Route.CWD},
			"sandbox_workspace_write.network_access":         false,
			"sandbox_workspace_write.exclude_tmpdir_env_var": true,
			"sandbox_workspace_write.exclude_slash_tmp":      true,
		}
	}
	if err := request("thread/start", threadParams, &started); err != nil {
		return result, err
	}
	result.ThreadID = started.Thread.ID
	if strings.TrimSpace(result.ThreadID) == "" {
		return result, errors.New("Codex did not return a thread ID")
	}
	if !started.Thread.Ephemeral || started.ApprovalPolicy != "on-request" || started.ApprovalsReviewer != "user" {
		return result, errors.New("Codex did not confirm ephemeral execution with user-controlled approvals")
	}
	actual := started.Sandbox
	if actual.NetworkAccess == nil || *actual.NetworkAccess || actual.Type != policy["type"] {
		return result, fmt.Errorf("Codex did not confirm the configured %s sandbox (reported %q); execution was not started", sandbox, actual.Type)
	}
	if sandbox == "workspace-write" {
		rootOK := len(actual.WritableRoots) == 1
		if rootOK {
			expected, expectedErr := filepath.EvalSymlinks(in.Route.CWD)
			returned, returnedErr := filepath.EvalSymlinks(actual.WritableRoots[0])
			rel, relErr := filepath.Rel(expected, returned)
			rootOK = expectedErr == nil && returnedErr == nil && relErr == nil && rel == "."
		}
		if !rootOK || !actual.ExcludeTmpdirEnvVar || !actual.ExcludeSlashTmp {
			return result, errors.New("Codex did not confirm writes limited to the configured workspace; execution was not started")
		}
	}
	record("session", result.ThreadID)
	var turn struct {
		Turn struct {
			ID string `json:"id"`
		} `json:"turn"`
	}
	params := map[string]any{"threadId": result.ThreadID, "input": []map[string]string{{"type": "text", "text": in.Prompt}}, "model": in.Route.Model, "cwd": in.Route.CWD, "approvalPolicy": "on-request", "approvalsReviewer": "user", "sandboxPolicy": policy}
	if in.Effort != "" {
		params["effort"] = in.Effort
	}
	if err := request("turn/start", params, &turn); err != nil {
		if ctx.Err() == nil || turnID == "" {
			return result, err
		}
	} else {
		if turn.Turn.ID == "" {
			return result, errors.New("Codex did not return a turn ID")
		}
		turnID = turn.Turn.ID
	}
	if terminalSeen {
		if ctx.Err() != nil {
			return result, ctx.Err()
		}
		return result, terminalError
	}
	cancelled := ctx.Done()
	var grace <-chan time.Time
	var cancelTimer *time.Timer
	defer func() {
		if cancelTimer != nil {
			cancelTimer.Stop()
		}
	}()
	for {
		incoming := queuedFrames()
		select {
		case <-cancelled:
			cancelled = nil
			record("progress", "Stopping Codex turn")
			interruptCtx, interruptCancel := context.WithTimeout(context.Background(), 5*time.Second)
			_, interruptErr := write(interruptCtx, "turn/interrupt", map[string]string{"threadId": result.ThreadID, "turnId": turnID})
			interruptCancel()
			if interruptErr != nil {
				return result, ctx.Err()
			}
			cancelTimer = time.NewTimer(5 * time.Second)
			grace = cancelTimer.C
		case <-grace:
			return result, ctx.Err()
		case r := <-incoming:
			if r.err != nil {
				if ctx.Err() != nil {
					return result, ctx.Err()
				}
				return result, fmt.Errorf("Codex protocol: %w", r.err)
			}
			terminal, err := handle(r.frame)
			if terminal || err != nil {
				if ctx.Err() != nil {
					return result, ctx.Err()
				}
				return result, err
			}
		}
	}
}
