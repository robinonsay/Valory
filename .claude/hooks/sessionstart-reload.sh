#!/usr/bin/env bash
# SessionStart hook (matchers: compact, resume) — re-inject the orchestrator's durable state.
#
# SessionStart stdout IS added to Claude's context. After a compaction or a resume, this gives
# the Software Lead orchestrator its live coordination state back from disk so it reconciles
# against the plan/ tree instead of trusting a lossy prose summary.
# See docs/agentic-architecture.md §10.4.
set -euo pipefail

DIR="${CLAUDE_PROJECT_DIR:-$(pwd)}"
STATE="$DIR/plan/state.json"

# No active effort tree -> stay quiet (no context noise for unrelated sessions).
[ -f "$STATE" ] || exit 0

echo "## Orchestrator state reloaded from disk (plan/state.json)"
echo
echo "You are resuming the Valory work-decomposition tree. Reconcile this live state against the"
echo "plan/ tree before acting, and trust disk over any prose summary. Root ask: see plan/root.md;"
echo "rules + worker schema: see CLAUDE.md; full model: docs/agentic-architecture.md."
echo
echo '```json'
cat "$STATE"
echo '```'

exit 0
