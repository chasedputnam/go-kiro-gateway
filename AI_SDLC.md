# AI-Assisted SDLC Guide

This document describes the end-to-end development workflow using the skills and agent personas available in this repository.

---

## The Skill Chain

The core workflow follows three sequential skills:

```
/spec  →  /dev  →  /code-review
```

Each skill hands off to the next. You own the approval gates between them.

---

## Phase 1: Spec (`/spec`)

Run `/spec` when you have a feature idea, bug to fix, or change to design. This is always the starting point — never jump straight to code for anything non-trivial.

What happens:
- You describe your idea in plain language
- The agent (backed by `product-owner` + `software-architect` + `project-manager`) guides you through three documents, one at a time:
  1. `specs/{feature}/requirements.md` — user stories and acceptance criteria in EARS format
  2. `specs/{feature}/design.md` — architecture, components, data models, error handling, testing strategy
  3. `specs/{feature}/tasks.md` — numbered, checkbox-style implementation tasks for a coding agent

You must explicitly approve each document before the workflow advances. If something looks off, give feedback and the agent revises until you're satisfied.

The spec workflow produces artifacts only — no code is written here.

---

## Phase 2: Dev (`/dev`)

Run `/dev` once your spec is approved. This skill reads all three spec documents and implements the tasks one at a time.

What happens:
- The agent (backed by `software-engineer`) reads `requirements.md`, `design.md`, and `tasks.md` before touching any code
- Tasks are executed in order, one at a time
- Each completed task is marked `[x]` in `tasks.md`
- The agent stops after each task and waits for your review before continuing

To run all tasks without stopping between them, tell the agent "execute all tasks." Otherwise it pauses after each one.

When `/dev` finishes, it prompts you to run `/code-review`.

---

## Phase 3: Code Review (`/code-review`)

Run `/code-review` after `/dev` completes. This skill reviews all changes made during the dev session against the spec requirements.

What happens:
- The agent (backed by `code-reviewer`) checks correctness, security, test coverage, clarity, and project conventions
- Findings are written to a timestamped markdown file in the spec directory
- Blocking issues must be resolved before you commit
- Once blocking items are cleared, you commit and open a merge request

---

## Full Workflow Example

```
1. You have an idea
2. /spec          → approve requirements.md
3. /spec          → approve design.md
4. /spec          → approve tasks.md
5. /dev           → implement task 1, review, continue...
6. /code-review   → fix blocking items
7. git commit + MR
```

---

## Agent Personas

These agents run as subagents during skill execution or can be invoked directly for specialized work. Each has a defined scope, model, and toolset.

### product-owner
Model: Opus | Tools: Read, Grep, Glob, Edit, Write

Translates business needs into clear, testable requirements. Use when you need to define acceptance criteria, evaluate whether a solution meets user needs, or pressure-test scope decisions.

Best for: requirements gathering, acceptance criteria, scope evaluation.

---

### software-architect
Model: Opus | Tools: Read, Grep, Glob, Edit, Write

Designs systems and makes technology decisions with an eye on trade-offs, operational burden, and long-term maintainability. Produces the `design.md` during `/spec`.

Best for: architecture decisions, component design, technology selection, design documents.

---

### project-manager
Model: Sonnet | Tools: Read, Grep, Glob

Breaks work into concrete, sequenced tasks and identifies risks and blockers. Produces `tasks.md` during `/spec`.

Best for: task planning, dependency sequencing, risk identification, progress tracking.

---

### software-engineer
Model: Sonnet | Tools: Read, Grep, Glob, Edit, Write, Bash

Implements features and fixes bugs by reading existing code first, matching project conventions, and writing the minimum code needed. Drives `/dev`.

Best for: feature implementation, bug fixes, refactoring, test writing.

---

### code-reviewer
Model: Sonnet | Tools: Read, Grep, Glob

Reviews diffs for correctness, security, test coverage, clarity, and adherence to project conventions. Distinguishes blocking issues from suggestions from nits.

Best for: PR review, pre-commit quality checks, catching issues before they reach production.

---

### quality-engineer
Model: Sonnet | Tools: Read, Grep, Glob, Bash

Designs test strategies, writes tests, identifies coverage gaps, and evaluates release readiness. Focuses on behavior and outcomes, not implementation details.

Best for: test strategy, coverage gap analysis, release readiness evaluation.

---

### security-reviewer
Model: Opus | Tools: Read, Grep, Glob (read-only — never makes changes)

Audits code for authentication flaws, injection risks, exposed secrets, container hardening issues, and supply chain risks. Reports findings with severity and remediation steps.

Best for: security audits, pre-release vulnerability review, infrastructure config review.

---

### pipeline-engineer
Model: Sonnet | Tools: Read, Grep, Glob, Edit, Write, Bash

Specializes in GitLab CI, Packer, Ansible, and container image build pipelines. Follows GitLab CI best practices and keeps runtime images minimal.

Best for: CI/CD configuration, Packer/Ansible work, pipeline debugging.

---

### teleport-expert
Model: Sonnet | Tools: Read, Grep, Glob

Deep expertise in Teleport Enterprise deployment — app service agents, tbot Machine ID, join tokens, Kubernetes Helm deployments, RBAC, and FIPS builds.

Best for: Teleport configuration questions, cluster enrollment, certificate-based access setup.

---

## When to Use Each Agent Directly

Most of the time the skills (`/spec`, `/dev`, `/code-review`) invoke the right agents automatically. Invoke an agent directly when you need focused, single-purpose help outside the main workflow:

| Situation | Agent to use |
|---|---|
| "Does this design make sense?" | software-architect |
| "Are these requirements complete?" | product-owner |
| "What's the test strategy for this?" | quality-engineer |
| "Is this code secure?" | security-reviewer |
| "Fix this CI pipeline job" | pipeline-engineer |
| "How do I configure this Teleport app agent?" | teleport-expert |
| "What tasks are left and what's blocked?" | project-manager |

---

## Spec File Structure

```
specs/
  {feature-name}/
    requirements.md   ← user stories + EARS acceptance criteria
    design.md         ← architecture, components, data models, error handling
    tasks.md          ← numbered checkbox implementation plan
```

Spec files support file references using `#[[file:<relative_path>]]` — useful for pulling in OpenAPI specs, GraphQL schemas, or other reference documents to inform the design.
