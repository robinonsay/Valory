#!/usr/bin/env bash
# PreCompact hook — snapshot the orchestrator's live coordination state before compaction.
#
# PreCompact stdout is debug-only (it is NOT injected into Claude's context), so this hook is a
# pure safety-net backup: it copies plan/state.json to a timestamped file under plan/.snapshots/.
# The post-compaction SessionStart hook is what re-injects state into context.
# See docs/agentic-architecture.md §10.4.
set -euo pipefail

DIR="${CLAUDE_PROJECT_DIR:-$(pwd)}"
STATE="$DIR/plan/state.json"
SNAPDIR="$DIR/plan/.snapshots"

if [ -f "$STATE" ]; then
  mkdir -p "$SNAPDIR"
  ts="$(date -u +%Y%m%dT%H%M%SZ)"
  cp "$STATE" "$SNAPDIR/state-$ts.json"
  echo "PreCompact: snapshotted plan/state.json -> plan/.snapshots/state-$ts.json"
fi

exit 0
