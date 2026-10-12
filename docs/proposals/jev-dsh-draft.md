# Upstream proposal draft — not sent

Magpie could expose a small structured decision endpoint for clients that already own tool execution and agent sessions. This fork tests OpenCode's genuine Jev Free through System One; dsh applies its decisions using native subagents and compaction transactions.

The boundary is explicit: Magpie serves models and decisions; the client starts, steers and collects agents. The fork removes the duplicate file executor. Raw history remains complete, including the tail previously lost after 1200 bytes. Ordinary execution requests keep their system prompt, tool schema and history order; context pressure can trigger a keep decision before any replacement.

The implementation and evidence are in the magpie-auto feature/auto-workspace branch and the independent dsh-auto repository. A fixed twelve-task SWE-bench Verified comparison uses one DeepSeek execution model and the same service path in every arm. It records actual cache hits/misses, native children and all decision/summary overhead. Failed cases remain. Final numbers and limitations will be added after grading; no efficiency claim is made in this draft.

This proposal is for maintainer review, not a request to merge an agent runtime into Magpie. The client plugin stays separate. A practical discussion would cover the decision API boundary and preserving client session metadata across local gateways.

Links: https://github.com/ZhiweiWei-NAMI/magpie-auto/tree/feature/auto-workspace and https://github.com/ZhiweiWei-NAMI/dsh-auto
