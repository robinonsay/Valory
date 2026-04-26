# Valory — Claude Code Project Guide

Valory is an AI professor system built on Go (backend), Vue.js (frontend), PostgreSQL, and the Anthropic SDK. It generates personalized courses, homework, and grading for any topic via a multi-agent architecture.

## Development agent architecture

This project is developed using a multi-agent pipeline defined in `.claude/agents/`. Each agent has a focused role and a defined place in the delivery pipeline.

### Pipeline overview

```
Project Manager  →  Software Lead  →  Contributors  →  SQE + Systems Engineer  →  Senior SQE  →  Deliver
```

| Agent file | Role |
|---|---|
| `project-manager` | Defines requirements; authors/maintains requirement JSON files |
| `software-lead` | Orchestrator; decomposes tasks, manages dependencies, drives the pipeline |
| `senior-engineer` | Implements complex or cross-cutting features |
| `junior-engineer` | Implements well-scoped, clearly-defined tasks |
| `requirements-author` | Authors and validates requirement JSON files |
| `test-author` | Writes test plans, unit tests, and integration tests |
| `design-author` | Produces TDDs, API specs, and data models |
| `software-quality-engineer` | First review gate: code quality, correctness, test coverage |
| `systems-engineer` | Second review gate (parallel with SQE): security, performance, integration |
| `senior-quality-engineer` | Final gate: cross-cutting quality and delivery approval |

### Failure loop

```
Contributor work
      |
      v
SQE + Systems Engineer review
      |
 Pass? +--- yes --> Senior SQE --> Deliver
      |
      no
      v
Return to contributor with feedback
      +---------> Review again
```

## Requirements

Requirements live as JSON files alongside the code they govern:

```
<module-directory>/requirements/REQ-<MODULE>-<NNN>.json
```

All requirement files must validate against `schemas/requirements.schema.json`. The `requirements-author` agent owns this schema and all requirement files.

## Tech stack

- **Backend:** Go — idiomatic, explicit error handling, `context.Context` throughout
- **Frontend:** Vue.js — Composition API, `<script setup>`, TypeScript
- **AI:** Anthropic SDK — Claude models via the Claude API
- **Database:** PostgreSQL — parameterized queries only, no SQL injection vectors
- **Infrastructure:** Docker + docker-compose

## Key conventions

- Always add comments so the *why* is obvious to a future reader
- No speculative abstractions — implement exactly what requirements specify
- No database mocks in integration tests — use a real PostgreSQL instance via Docker Compose
- AsciiDoc course content: max 500 lines per document, use `include::` for composition
- Secrets and API keys come from environment variables only — never hardcoded
