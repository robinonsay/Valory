#!/usr/bin/env python3
"""discovery.py — traversal + mutation tool for the Valory discovery phase.

The discovery phase (see docs/discovery-phase.md) is a depth-first, disk-durable question tree
that runs before a decomposition boundary whenever the triage flags uncertainty. Its durable
runtime is plan/discovery/<node-id>/frontier.json, validated by
schemas/discovery-frontier.schema.json.

The DFS mechanics — pop the highest-weight open question, attach children, prune subsumed
siblings, check the budget backstop — are deterministic, so they must NOT be hand-maintained as
JSON inside the orchestrator's context (that is how frontier.json drifts out of spec). This tool
owns every mutation and keeps the file always valid. Stdlib-only, no dependencies.

Loop (orchestrator side):
    next   -> pop the highest-weight open question (depth-first priority)
    add    -> attach the child questions the answer raised (with weights)
    prune  -> discard siblings the answer subsumed, before fanning out wider
    answer -> mark a question resolved (leaf w/ reason, or non-leaf w/ children)
    check  -> budget backstop + structural audit; recommends escalation
    status -> one-glance summary; tree -> the whole DFS shape
    done   -> transition the pass to 'done' once it is sufficient to decompose

Run `discovery.py <command> -h` for per-command help.
"""

from __future__ import annotations

import argparse
import datetime as _dt
import json
import sys
from pathlib import Path

# ---------------------------------------------------------------------------
# IO helpers
# ---------------------------------------------------------------------------

VALID_STATUS = {"discovering", "gated", "done", "escalated"}
VALID_Q_STATUS = {"open", "answered", "pruned", "inconclusive"}


def _now() -> str:
    return _dt.datetime.now(_dt.timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")


def _die(msg: str, code: int = 1) -> "None":
    print(f"error: {msg}", file=sys.stderr)
    raise SystemExit(code)


def load(path: Path) -> dict:
    if not path.exists():
        _die(f"frontier not found: {path}")
    try:
        return json.loads(path.read_text())
    except json.JSONDecodeError as e:
        _die(f"frontier is not valid JSON ({e}): {path}")


def save(path: Path, fr: dict) -> None:
    fr["updated_at"] = _now()
    path.write_text(json.dumps(fr, indent=2) + "\n")


def questions(fr: dict) -> list[dict]:
    return fr.get("questions", [])


def find_q(fr: dict, qid: str) -> dict:
    for q in questions(fr):
        if q["id"] == qid:
            return q
    _die(f"no such question: {qid}")


def next_qid(fr: dict) -> str:
    n = 0
    for q in questions(fr):
        try:
            n = max(n, int(q["id"][1:]))
        except (ValueError, KeyError):
            pass
    return f"Q{n + 1}"


# ---------------------------------------------------------------------------
# commands
# ---------------------------------------------------------------------------


def cmd_init(args: argparse.Namespace) -> int:
    path = Path(args.file)
    if path.exists() and not args.force:
        _die(f"refusing to overwrite existing frontier (use --force): {path}")
    path.parent.mkdir(parents=True, exist_ok=True)
    fr = {
        "node": args.node,
        "status": "discovering",
        "updated_at": _now(),
        "budget": {
            "max_depth": args.max_depth,
            "max_open": args.max_open,
            "token_cap": args.token_cap,
        },
        "tokens_spent": 0,
        "questions": [],
    }
    save(path, fr)
    print(f"initialized discovery frontier for node '{args.node}' -> {path}")
    return 0


def cmd_add(args: argparse.Namespace) -> int:
    path = Path(args.file)
    fr = load(path)
    parent = None
    depth = 0
    if args.parent:
        parent = find_q(fr, args.parent)
        depth = parent["depth"] + 1
    qid = next_qid(fr)
    q = {
        "id": qid,
        "parent": args.parent,
        "depth": depth,
        "weight": args.weight,
        "question": args.question,
        "status": "open",
        "answer_ref": None,
        "children": [],
        "leaf_reason": None,
        "pruned_by": None,
    }
    fr["questions"].append(q)
    if parent is not None and qid not in parent["children"]:
        parent["children"].append(qid)
    save(path, fr)
    flag = ""
    if depth > fr["budget"]["max_depth"]:
        flag = "  [!] exceeds max_depth — run `check`"
    print(f"added {qid} (depth {depth}, weight {args.weight:.2f}){flag}")
    return 0


def _open_questions(fr: dict) -> list[dict]:
    return [q for q in questions(fr) if q["status"] == "open"]


def cmd_next(args: argparse.Namespace) -> int:
    fr = load(Path(args.file))
    openq = _open_questions(fr)
    if not openq:
        print("frontier empty: no open questions")
        # Distinct exit code so a loop can detect the pass-stop condition.
        return 3
    # Depth-first PRIORITY: highest weight first; tie-break toward the deeper question
    # (continue the current spine), then lowest id for determinism.
    openq.sort(key=lambda q: (-q["weight"], -q["depth"], int(q["id"][1:])))
    q = openq[0]
    print(f"{q['id']}  (depth {q['depth']}, weight {q['weight']:.2f})")
    print(q.get("question", "<no text>"))
    return 0


def cmd_answer(args: argparse.Namespace) -> int:
    path = Path(args.file)
    fr = load(path)
    q = find_q(fr, args.id)
    if q["status"] == "pruned":
        _die(f"{args.id} was pruned; cannot answer")
    q["status"] = "answered"
    q["answer_ref"] = args.answer_ref or f"{args.id}.md"
    if args.leaf_reason:
        q["leaf_reason"] = args.leaf_reason
    if q["children"] and args.leaf_reason:
        _die(f"{args.id} has children but was given a leaf_reason; a leaf has no children")
    if not q["children"] and not args.leaf_reason:
        print(
            f"warning: {args.id} answered with no children and no --leaf-reason. "
            "Add children with `add --parent`, or record why it is a leaf with --leaf-reason "
            "(the gate audits this).",
            file=sys.stderr,
        )
    save(path, fr)
    kind = "leaf" if q["leaf_reason"] else "node"
    print(f"answered {args.id} ({kind}) -> {q['answer_ref']}")
    return 0


def _descendants(fr: dict, qid: str) -> list[str]:
    out: list[str] = []
    for child in find_q(fr, qid)["children"]:
        out.append(child)
        out.extend(_descendants(fr, child))
    return out


def cmd_prune(args: argparse.Namespace) -> int:
    path = Path(args.file)
    fr = load(path)
    find_q(fr, args.by)  # validate the subsuming question exists
    targets = [args.id]
    if args.cascade:
        targets += _descendants(fr, args.id)
    pruned = []
    for tid in targets:
        q = find_q(fr, tid)
        if q["status"] in ("answered",):
            continue  # never retroactively prune an answered question
        q["status"] = "pruned"
        q["pruned_by"] = args.by
        pruned.append(tid)
    save(path, fr)
    print(f"pruned {', '.join(pruned) if pruned else '(none)'} by {args.by}")
    return 0


def cmd_tokens(args: argparse.Namespace) -> int:
    path = Path(args.file)
    fr = load(path)
    fr["tokens_spent"] = fr.get("tokens_spent", 0) + args.add
    save(path, fr)
    print(f"tokens_spent = {fr['tokens_spent']} / {fr['budget']['token_cap']}")
    return 0


def _audit(fr: dict) -> list[str]:
    """Structural + budget findings. Empty list == clean."""
    issues: list[str] = []
    ids = [q["id"] for q in questions(fr)]
    if len(ids) != len(set(ids)):
        issues.append("duplicate question ids")
    idset = set(ids)
    for q in questions(fr):
        if q.get("parent") and q["parent"] not in idset:
            issues.append(f"{q['id']}: parent {q['parent']} missing")
        for c in q.get("children", []):
            if c not in idset:
                issues.append(f"{q['id']}: child {c} missing")
        if q["status"] == "answered" and not q["children"] and not q.get("leaf_reason"):
            issues.append(f"{q['id']}: answered leaf with no leaf_reason (premature stop?)")
        if q["status"] == "answered" and q["children"] and q.get("leaf_reason"):
            issues.append(f"{q['id']}: has children AND a leaf_reason (contradiction)")
        if q["status"] == "pruned" and not q.get("pruned_by"):
            issues.append(f"{q['id']}: pruned with no pruned_by (un-auditable prune)")
        if q["depth"] > fr["budget"]["max_depth"]:
            issues.append(f"{q['id']}: depth {q['depth']} exceeds max_depth {fr['budget']['max_depth']}")
    open_n = len(_open_questions(fr))
    if open_n > fr["budget"]["max_open"]:
        issues.append(f"open questions {open_n} exceed max_open {fr['budget']['max_open']}")
    if fr.get("tokens_spent", 0) > fr["budget"]["token_cap"]:
        issues.append(f"tokens_spent {fr['tokens_spent']} exceed token_cap {fr['budget']['token_cap']}")
    return issues


def cmd_check(args: argparse.Namespace) -> int:
    fr = load(Path(args.file))
    issues = _audit(fr)
    budget_breached = any(
        s in i for i in issues for s in ("exceeds max_depth", "exceed max_open", "exceed token_cap")
    )
    if not issues:
        print("check: clean — no structural or budget issues")
        return 0
    print("check: issues found")
    for i in issues:
        print(f"  - {i}")
    if budget_breached:
        print(
            "\nA budget cap is breached. This pass is INCONCLUSIVE — set status 'escalated' "
            "(`discovery.py done --to escalated`), record the residual open questions as logged "
            "assumptions, and either widen the budget or proceed explicitly. A budget stop is "
            "NEVER a clean exit (docs/discovery-phase.md §5)."
        )
        return 4
    return 4


def cmd_status(args: argparse.Namespace) -> int:
    fr = load(Path(args.file))
    qs = questions(fr)
    counts = {s: sum(1 for q in qs if q["status"] == s) for s in VALID_Q_STATUS}
    max_depth = max((q["depth"] for q in qs), default=0)
    decomposable = (
        fr["status"] == "done" and counts["open"] == 0 and not _audit(fr)
    )
    print(f"node:          {fr['node']}")
    print(f"pass status:   {fr['status']}")
    print(f"questions:     {len(qs)} total  |  "
          + "  ".join(f"{s}={counts[s]}" for s in ("open", "answered", "pruned", "inconclusive")))
    print(f"max depth:     {max_depth} / {fr['budget']['max_depth']}")
    print(f"open:          {counts['open']} / {fr['budget']['max_open']}")
    print(f"tokens:        {fr.get('tokens_spent', 0)} / {fr['budget']['token_cap']}")
    print(f"decomposable:  {'YES' if decomposable else 'no'}"
          + ("" if decomposable else "  (needs status=done, 0 open, clean check)"))
    return 0


_MARK = {"open": "?", "answered": "+", "pruned": "x", "inconclusive": "!"}


def _print_subtree(fr: dict, qid: str, prefix: str) -> None:
    q = find_q(fr, qid)
    text = (q.get("question") or "").strip().replace("\n", " ")
    if len(text) > 70:
        text = text[:67] + "..."
    extra = ""
    if q["status"] == "pruned" and q.get("pruned_by"):
        extra = f"  (pruned by {q['pruned_by']})"
    elif q.get("leaf_reason"):
        extra = "  [leaf]"
    print(f"{prefix}{_MARK[q['status']]} {q['id']} (w{q['weight']:.2f}) {text}{extra}")
    for c in q["children"]:
        _print_subtree(fr, c, prefix + "    ")


def cmd_tree(args: argparse.Namespace) -> int:
    fr = load(Path(args.file))
    roots = [q["id"] for q in questions(fr) if not q.get("parent")]
    print(f"{fr['node']}  [{fr['status']}]   legend: + answered  ? open  x pruned  ! inconclusive")
    for r in roots:
        _print_subtree(fr, r, "  ")
    return 0


def cmd_done(args: argparse.Namespace) -> int:
    path = Path(args.file)
    fr = load(path)
    target = args.to
    if target not in VALID_STATUS:
        _die(f"invalid status '{target}' (one of: {', '.join(sorted(VALID_STATUS))})")
    if target == "done":
        openq = _open_questions(fr)
        if openq:
            _die(f"cannot mark done: {len(openq)} open question(s) remain "
                 f"({', '.join(q['id'] for q in openq)}). Resolve or prune them first.")
        issues = _audit(fr)
        if issues:
            _die("cannot mark done: `check` is not clean. Run `discovery.py check` and resolve.")
    fr["status"] = target
    save(path, fr)
    print(f"pass status -> {target}")
    if target == "done":
        print("Findings are ready to decompose. Run the discovery-gate, then write findings.md.")
    return 0


def cmd_validate(args: argparse.Namespace) -> int:
    """Structural validation. Uses jsonschema if installed; always runs the built-in audit."""
    path = Path(args.file)
    fr = load(path)
    ok = True
    # 1. top-level shape
    for key in ("node", "status", "budget", "questions"):
        if key not in fr:
            print(f"  - missing required key: {key}")
            ok = False
    if fr.get("status") not in VALID_STATUS:
        print(f"  - invalid status: {fr.get('status')}")
        ok = False
    # 2. optional formal schema check
    try:
        import jsonschema  # type: ignore

        schema_path = Path(__file__).resolve().parent.parent / "schemas" / "discovery-frontier.schema.json"
        if schema_path.exists():
            jsonschema.validate(fr, json.loads(schema_path.read_text()))
            print("jsonschema: valid against schemas/discovery-frontier.schema.json")
    except ImportError:
        print("(jsonschema not installed — ran built-in structural checks only)")
    except Exception as e:  # jsonschema.ValidationError and friends
        print(f"  - schema violation: {e}".split("\n")[0])
        ok = False
    # 3. semantic audit
    for i in _audit(fr):
        print(f"  - {i}")
        ok = False
    print("validate: OK" if ok else "validate: FAILED")
    return 0 if ok else 5


# ---------------------------------------------------------------------------
# arg parsing
# ---------------------------------------------------------------------------


def build_parser() -> argparse.ArgumentParser:
    p = argparse.ArgumentParser(
        prog="discovery.py",
        description="Traversal + mutation tool for a Valory discovery frontier (docs/discovery-phase.md).",
    )
    sub = p.add_subparsers(dest="command", required=True)

    def add_file_arg(sp: argparse.ArgumentParser) -> None:
        sp.add_argument("file", help="path to the frontier.json (e.g. plan/discovery/root/frontier.json)")

    sp = sub.add_parser("init", help="create a new frontier.json for a node")
    add_file_arg(sp)
    sp.add_argument("--node", required=True, help="tree node: root | G<n> | G<n>-S<m> | G<n>-S<m>-T<k>")
    sp.add_argument("--max-depth", type=int, default=4)
    sp.add_argument("--max-open", type=int, default=20)
    sp.add_argument("--token-cap", type=int, default=150000)
    sp.add_argument("--force", action="store_true", help="overwrite an existing frontier")
    sp.set_defaults(func=cmd_init)

    sp = sub.add_parser("add", help="add an open question (a triage seed, or a child of --parent)")
    add_file_arg(sp)
    sp.add_argument("--question", required=True)
    sp.add_argument("--weight", type=float, required=True, help="constraint_weight in [0,1]")
    sp.add_argument("--parent", help="parent question id (omit for a triage-seeded root question)")
    sp.set_defaults(func=cmd_add)

    sp = sub.add_parser("next", help="print the highest-weight open question (DFS priority pop)")
    add_file_arg(sp)
    sp.set_defaults(func=cmd_next)

    sp = sub.add_parser("answer", help="mark a question answered (leaf with --leaf-reason, else attach children via `add`)")
    add_file_arg(sp)
    sp.add_argument("id", help="question id, e.g. Q3")
    sp.add_argument("--leaf-reason", help="why this answer is decision-stable (makes it a leaf)")
    sp.add_argument("--answer-ref", help="path to the answer file (default <id>.md)")
    sp.set_defaults(func=cmd_answer)

    sp = sub.add_parser("prune", help="mark a sibling (and optionally its subtree) subsumed by an answer")
    add_file_arg(sp)
    sp.add_argument("id", help="question id to prune")
    sp.add_argument("--by", required=True, help="id of the question whose answer subsumed it")
    sp.add_argument("--cascade", action="store_true", help="also prune the question's open descendants")
    sp.set_defaults(func=cmd_prune)

    sp = sub.add_parser("tokens", help="increment tokens_spent")
    add_file_arg(sp)
    sp.add_argument("--add", type=int, required=True)
    sp.set_defaults(func=cmd_tokens)

    sp = sub.add_parser("check", help="budget backstop + structural audit (exit 4 if escalation needed)")
    add_file_arg(sp)
    sp.set_defaults(func=cmd_check)

    sp = sub.add_parser("status", help="one-glance summary of the pass")
    add_file_arg(sp)
    sp.set_defaults(func=cmd_status)

    sp = sub.add_parser("tree", help="render the DFS question tree")
    add_file_arg(sp)
    sp.set_defaults(func=cmd_tree)

    sp = sub.add_parser("done", help="transition the pass status (done | gated | escalated | discovering)")
    add_file_arg(sp)
    sp.add_argument("--to", default="done", help="target status (default: done)")
    sp.set_defaults(func=cmd_done)

    sp = sub.add_parser("validate", help="validate the frontier against the schema + semantic audit")
    add_file_arg(sp)
    sp.set_defaults(func=cmd_validate)

    return p


def main(argv: list[str] | None = None) -> int:
    args = build_parser().parse_args(argv)
    return args.func(args)


if __name__ == "__main__":
    raise SystemExit(main())
