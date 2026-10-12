# Magpie Auto

Auto is opt-in. With Auto disabled, existing Magpie routing, agents and Sessions keep their normal behavior. Auto owns a separate task journal and its own executor processes. Sessions continues to show external history; Auto does not rewrite that history.

## Run privately

Use Go 1.26.8 or newer. Keep this checkout and build separate from an installed Magpie.

```sh
go build -tags nogui -o dist/magpie-auto .
mkdir -p dist/data
MAGPIE_ADDR=127.0.0.1:3435 ./dist/magpie-auto web --addr 127.0.0.1:3436
```

On Windows:

```powershell
go build -tags nogui -o dist/magpie-auto.exe .
New-Item -ItemType Directory -Force dist/data
$env:MAGPIE_ADDR = '127.0.0.1:3435'
./dist/magpie-auto.exe web --addr 127.0.0.1:3436
```

Open the private sign-in link printed by Magpie. The `data` folder beside the binary activates upstream portable storage, avoiding an installed Magpie's configuration. Select **Auto**. The standard Windows desktop build is also supported with `go build -o dist/magpie-auto.exe .`; Linux desktop builds need upstream's native GTK/WebKit prerequisites.

Do not bind the console to a public address. Auto routes are behind the same authenticated GUI handler as existing Magpie settings. The run key remains local.

## Configure

Add your Jev endpoint/key using the existing **Providers** page. Auto's configuration contains a decision-model route ID, never an API key. In **Configure Auto**, enable Auto, select the configured Jev route (for example `my-jev/jev-latest`), and supply a Codex executable, model, and absolute working directory. Advanced configuration accepts several routes:

```json
{
  "enabled": true,
  "decider": "my-jev/jev-latest",
  "maxConcurrent": 1,
  "contextBytes": 16000,
  "routes": [{
    "id": "codex-review",
    "adapter": "codex-app-server",
    "description": "Read-only source analysis",
    "command": "/absolute/path/to/codex",
    "args": ["app-server"],
    "cwd": "/absolute/path/to/project",
    "sandbox": "read-only",
    "model": "your-configured-model",
    "efforts": ["low", "medium", "high"]
  }]
}
```

Use a real model supported by that executor's local configuration. Magpie does not install or sign in an executor from this form. Models routed through Magpie require the executor's existing supported Magpie connection. Route configuration describes the executor's actual model IDs and supported effort levels; an invalid model returns an execution error.

Missing Jev configuration is shown as unconfigured. A rejected or malformed decision fails the run; it does not fall back to a fabricated successful choice.

## Operate

Create a task, then choose **Start**. Jev receives the current goal/request, structured state, eligible executor routes, quality/cost/speed preferences, bounded task feedback scores and a bounded set of source excerpts. Its typed decisions select route, effort, execute/child action and relevant source IDs. Go validates and executes those decisions. One-run route/effort choices restrict the candidates; current requests take priority over saved preferences and learned scores.

**Continue** adds a run to the same durable task. Each run creates a fresh owned native executor thread with selected source excerpts and compact current state. This keeps execution context bounded instead of restoring an unlimited native thread history. The default context budget is 16,000 bytes; configured budgets are capped at 64,000 bytes. Raw events remain durable separately from the selected execution context.

The full goal and current request are reserved before optional history. A request that cannot fit the configured execution budget is rejected before routing or execution. Structured state records the current request and constraints, progress, completed result excerpts, pending dependencies and source references. Result excerpts link to the durable full output. Delegated runs retain the first validated route/effort decision, and the parent displays the executed child's answer, context and artifacts with their original provenance. Dependency context is refreshed from the latest completed run.

**Stop** cancels an owned run (or its owned child), requests native turn interruption, then kills only its process tree if necessary. Navigating away or an HTTP disconnect does not cancel a task. Queued independent tasks run within the configured 1–4 execution limit; explicit dependencies must finish before their dependent runs begin. A restart marks unfinished runs interrupted; Continue creates a new bounded execution.

Task preferences and feedback affect subsequent decisions for that task. Feedback updates bounded routing preference scores and supported request parameters. It does not change a remote model's neural weights or tool permissions.

## Supported capabilities

The adapter is **Codex app-server**, using its initialize, thread/start, turn/start and turn/interrupt protocol with owned-process cleanup. It streams commentary, final answers and progress separately and records source events and artifact provenance. Routes default to a **read-only sandbox**. Explicitly choosing `workspace-write` permits edits inside that route's working directory; the UI requires confirmation of the exact directory. Network access remains disabled and full-access routes are rejected. Jev cannot expand a route's permissions.

The adapter checks the executor's effective sandbox before starting a turn. If the installed client reduces or expands the requested scope, the run fails visibly. In this Windows delivery environment, Codex 0.162.1 reported read-only for workspace-write requests; native editing was therefore unavailable here. Read-only handshake and fixture-backed workspace-write behavior were verified. No live model execution was performed.

Native file-change approval requests can be approved once when every recorded path is contained in an explicitly configured writable workspace. Requests outside that scope can only be declined or stopped. Command escalation requests offer Decline and Stop because the supported protocol does not guarantee a bounded sandbox for accepting an escalation. Native questions appear in the conversation; secret answers use password inputs and are not saved in Magpie task records or drafts. Owned threads use the executor's ephemeral storage mode. Answers still reach the selected model; its provider's retention policy applies. Pending requests expire when their owning run ends or is stopped. Other Magpie clients remain available in normal mode and history views; they are not advertised as controllable Auto lifecycle adapters. No external agent's CLI history is changed.

Artifacts come from recorded executor file-change events. Downloads resolve symlinks and allow only recorded regular files within the original run workspace, including when a parent task displays a child's artifacts. Analysis runs may have no file artifacts; their messages and provenance remain in the task journal.

## Storage and verification

Auto stores `state.json` and an append-only `events.jsonl` in the portable/configuration directory's `auto` folder. They contain private task text and outputs; do not commit them. Jev credentials remain in upstream Magpie's provider configuration.

Core checks:

```sh
go test -tags nogui ./internal/auto ./internal/gui
go vet -tags nogui ./...
go build -tags nogui .
```

The protocol and Jev HTTP fixtures test local contracts without a paid model call. They are not evidence of live Jev/model execution. See the accompanying validation record for checks actually run in the delivery environment.
