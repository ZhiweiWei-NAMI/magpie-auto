# Auto decisions and dsh execution

This fork keeps Magpie as a model gateway and decision service. dsh owns tool execution, agent sessions, native children, message delivery and child collection. Magpie does not run a second file or shell agent.

`internal/auto/jev.go` is the shared Jev client for durable inference tasks and the dsh decision API. It sends `model`, a complete string `state`, and typed `questions` to OpenCode's `/inference/v1/systemone`. The model is fixed to `jev-1.13-free`; Console authentication is read from a private local key file. No chat-completions request or paid-model fallback is used for Jev.

`POST /v1/auto/decide` in `internal/gateway/auto_plan.go` accepts:

```json
{
  "sessionId": "native-session-id",
  "task": "complete current task",
  "trigger": "task_start",
  "history": [{"id": "event_12", "kind": "assistant", "text": "complete source", "required": false}],
  "actions": {"single": "one coding agent", "review": "independent investigation"},
  "observation": {"nativeUsage": {"inputTokens": 300, "cacheReadTokens": 200}}
}
```

The response contains the actual `model`, `answers` and `usage` when provided, the selected action and source IDs, and measured Jev elapsed time. Source IDs are emitted in their original order. Required user instructions stay; uncertain evidence with `noul >= 0.35` stays. Missing or malformed primitive answers fail the request and never become zero. Errors retain an observed HTTP status and redact the Console key.

The console stores Auto configuration in `<Magpie data directory>/auto/state.json`. `enabled`, `decider` and `jevKeyFile` select the decision service. A disabled or incorrectly configured service returns an explicit error. Existing inference task journals remain available; their source text is complete rather than the first 1200 bytes. Unsupported workspace/executor adapters are rejected.

The client implementation is [dsh-auto](https://github.com/ZhiweiWei-NAMI/dsh-auto). Its profile configuration controls activation, context pressure and a task's child budget. It calls the native `ctx.subagents` service and the native compaction transaction. Ordinary dsh requests retain system instructions, tool definitions and chronological history; new content is appended. A balanced old region is considered only under pressure. A small routing call first chooses keep or select; only select invokes complete-source scoring batches. Those batches use a conservative 24,000-byte state bound under Jev's documented 32k-token state-plus-longest-question limit. An oversized source is judged in all its Unicode-safe parts; any retained part retains the entire original source. A useful selection may replace that region, while a keep decision preserves the warmed prefix. The append-only raw journal is retained in either case.

For the existing `dsh-workbuddy` connection, the gateway forwards the native `x-deepseek-harness-session-id` as `x-wb-session-id`. This lets the existing local WorkBuddy service keep the execution session on its bound account. Jev requests have their own context and are never inserted into the execution transcript.

Experiment records use actual execution-service usage. DeepSeek's `prompt_tokens` includes cache hits; `prompt_cache_hit_tokens` and `prompt_cache_miss_tokens` identify read and miss quantities. Compatibility fields that disagree with these canonical fields are not substituted for them. Absent fields remain unobserved. Context-pressure estimates are not billing measurements, and the existing service's credits are not converted into dollars without a verified billing basis.

Verification includes full-history replay, typed Jev response and failure tests, native dsh tool execution, child collection, and the fixed SWE-bench Verified subset in `experiments/jev-cache-20261012`.
