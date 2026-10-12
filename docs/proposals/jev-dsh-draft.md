# Upstream proposal draft — not sent

Magpie could expose a small structured decision endpoint for clients that already own tool execution and agent sessions. This fork tests OpenCode's genuine Jev Free through System One; dsh applies its decisions using native subagents and compaction transactions.

The boundary is explicit: Magpie serves models and decisions; the client starts, steers and collects agents. The fork removes the duplicate file executor. Raw history remains complete, including the tail previously lost after 1200 bytes. Ordinary execution requests keep their system prompt, tool schema and history order; context pressure can trigger a keep decision before any replacement.

The implementation and evidence are in the magpie-auto feature/auto-workspace branch and the independent dsh-auto repository. All 36 runs on twelve predetermined SWE-bench Verified tasks were graded with the official harness. Auto resolved 6/12; single-agent and optional-native-child controls each resolved 8/12. Auto started three native investigators: one completed, and two failed on the same task. The optional-child control chose single-agent execution throughout. All 71 Jev calls succeeded and added 56.09 seconds of API time.

On the five tasks all three arms resolved, Auto used 12.7% fewer uncached input tokens than the single-agent control, but generated 27.9% more output and took 6.7% longer. This does not establish overall savings. Jev chose to keep history at every pressure decision, so applying a real Jev selection through native compaction remains unverified. The upstream execution gateway still rotates accounts within a session; remote caches were not reset. The test also uses a lower compaction threshold than default dsh and only one project with one run per task. Missing usage from cancelled streams is retained as unobserved, and no dollar costs are inferred.

This proposal is for maintainer review, not a request to merge an agent runtime into Magpie. The client plugin stays separate. A practical discussion would cover the decision API boundary and preserving client session metadata across local gateways.

Links: https://github.com/ZhiweiWei-NAMI/magpie-auto/tree/feature/auto-workspace and https://github.com/ZhiweiWei-NAMI/dsh-auto
