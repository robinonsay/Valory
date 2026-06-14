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
DISCDIR="$DIR/plan/discovery"

# Collect any in-flight discovery passes (status 'discovering'), skipping the _TEMPLATE scaffold.
discovering=""
if [ -d "$DISCDIR" ]; then
  for f in "$DISCDIR"/*/frontier.json; do
    [ -f "$f" ] || continue
    case "$(basename "$(dirname "$f")")" in _*) continue ;; esac
    if grep -q '"status": "discovering"' "$f"; then
      discovering="$discovering $f"
    fi
  done
fi

# No active effort tree and no live discovery -> stay quiet (no context noise for unrelated sessions).
[ -f "$STATE" ] || [ -n "$discovering" ] || exit 0

if [ -f "$STATE" ]; then
  echo "## Orchestrator state reloaded from disk (plan/state.json)"
  echo
  echo "You are resuming the Valory work-decomposition tree. Reconcile this live state against the"
  echo "plan/ tree before acting, and trust disk over any prose summary. Root ask: see plan/root.md;"
  echo "rules + worker schema: see CLAUDE.md; full model: docs/agentic-architecture.md."
  echo
  echo '```json'
  cat "$STATE"
  echo '```'
fi

# Surface any in-flight discovery pass so it is RESUMED from disk, not restarted
# (docs/discovery-phase.md §10).
if [ -n "$discovering" ]; then
  echo
  echo "## Discovery passes in progress (resume from disk — do not restart)"
  echo
  for f in $discovering; do
    node="$(basename "$(dirname "$f")")"
    echo "- node \`$node\`: resume with \`python3 scripts/discovery.py next plan/discovery/$node/frontier.json\`, then dispatch a discovery-agent. Inspect with \`discovery.py tree\` / \`status\`."
  done
fi

exit 0
