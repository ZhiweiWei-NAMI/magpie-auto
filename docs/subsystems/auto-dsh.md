# Auto decisions and dsh execution

Magpie serves models, compiles user routing preferences, and calls Jev. dsh owns tool execution, native agent sessions, continuations, message delivery and collection. The independent [dsh-auto](https://github.com/ZhiweiWei-NAMI/dsh-auto) plugin applies decisions through those services; it has no second execution runtime.

## API and configuration

[`internal/auto/jev.go`](../../internal/auto/jev.go) sends `model`, a string `state` and typed `questions` to OpenCode `/inference/v1/systemone`. The model remains `jev-1.13-free`. The Console key is read locally and redacted from upstream errors. There is no paid-model fallback or automatic retry. Actual answers, usage, model and measured elapsed time are preserved; absent values are not synthesized.

`POST /v1/auto/decide` accepts the complete `task`, `sessionId`, `trigger`, candidate `history`, `observation`, and optional `questions`. Atomic questions use the official `choice`, `score` and `noul` primitives. Descriptive criteria are mandatory for choice and score. Questions are not duplicated into state. Missing confidence, probabilities or primitive values fail validation rather than becoming zero. Legacy `actions` / per-source noul requests remain available to existing inference-task callers, but cannot be mixed with custom questions.

`POST /api/auto/config` stores configuration in `<Magpie data directory>/auto/state.json`. Its `strategyPrompt` is compiled into a mode, four objective preferences and per-route initial preferences by genuine Jev. The saved `strategy` retains the actual decision. A caller cannot supply fabricated strategy scores. Unchanged prompt and routes reuse the saved result; clearing the prompt clears the strategy. Configuration API errors preserve the prior saved state.

`GET /v1/auto/policy` exposes enabled status, allowed routes, instruction and compiled strategy, never the key file or secret. Clients read it once per root session and persist their snapshot in the native dsh storage domain `dsh_auto`. Changing the console does not rewrite an active session's system prompt or tools. Current testing uses one actual model/effort, DeepSeek v4.1 Flash High; generic profile selection is not evidence of cross-model performance gains.

## Native client behavior

The execution model proposes a concrete subtask with its existing native tool. Jev judges dependency, unfinished value, profile fit and continuity separately. Deterministic rules then decline delegation, create a native continuable child, or message an existing child. It does not ask Jev to generate arbitrary task text. Background children can work alongside the main agent; explicit foreground calls wait for real results. Children retain model, effort, persona, tool definitions and chronological history across activations.

Child count, concurrency, total child model requests, per-activation requests and Jev requests have explicit limits. Model calls for native compaction also consume the budget. All experimental arms share the same task-level cap; A and C share child limits. Budgeted child failure is recorded and leaves the main agent available to finish. Native `send_message` delivers actual findings before a budget-limited reporting turn is concluded. Durable policy and assignment metadata use the SDK storage domain, not unrecognized native session events.

A compiled fixed-single or fixed-native policy skips task-level Jev decisions and uses native compaction. Direct plugin `single` / `native` modes also bypass Jev and require the ordinary `compaction-basic` plugin enabled. The one-time configuration call is accounted separately.

## Context and cache

Normal requests append new content. The plugin uses the most recent actual API input as an anchor and estimates only new growth for pressure control; missing usage is explicitly handled as a native estimate, never billing data. Old dsh estimates exceeded aligned actual inputs by a median factor of 2.04 in the earlier experiment.

Under pressure, a balanced old region is split into complete logical content blocks. Task requirements are pinned. Jev scores importance and responsibility separately, using complete recent evidence where it fits the state budget. Candidates over the conservative 24,000-byte state bound are fully divided on Unicode boundaries. Keeping any part keeps its whole logical source. Uncertain evidence stays. Ownership is used for retention and audit; it is not a claim that a source has already been sent to another child.

Only sufficient estimated reduction triggers a native compaction transaction. Otherwise history stays and the next decision threshold rises. Quality preference makes uncertain removal and delegation more conservative. Cache preference raises the required reduction and the interval between unchanged-context decisions. Raw journals remain complete. Replacing history necessarily breaks part of its prefix; stable system/tools do not guarantee a cache hit.

When the decision budget cannot cover all complete source batches, the plugin explicitly records a native-compaction boundary. It does not silently truncate the remaining candidates or invent a Jev keep answer. HTTP errors remain errors.

The gateway forwards native `x-deepseek-harness-session-id` as `x-wb-session-id`. The shared 8788 runtime still reports `least_inflight` and changes account labels within sessions; it was not restarted. Account affinity is therefore not established, and account changes alone do not prove cache isolation. Jev requests are separate from execution sessions.

## Evidence

Usage accounting uses actual API fields: DeepSeek `prompt_tokens`, `prompt_cache_hit_tokens`, `prompt_cache_miss_tokens` and `completion_tokens`. Compatibility cache-write zero aliases do not establish native write observations. Missing counters stay unobserved. Model token totals express workload, not interchangeable cost; no dollar conversion is made without verified pricing.

[The original twelve-task experiment](../../experiments/jev-cache-20261012/) and [the routing revision](../../experiments/jev-routing-revision/) are separate. The latter includes real continuation, actual context replacement, failed development probes and a fixed post-hoc two-task diagnostic. Numbered metrics M1–M7 cover quality, time, total usage, cache, decision overhead, stability and collaboration. The larger original experiment used v0.2.2 and did not establish an Auto benefit.
