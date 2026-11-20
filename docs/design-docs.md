# Writing a design doc

Status: standing rule.

A design doc settles what a feature does before the code is written.
It is for the next author and the reviewer: they read it to learn what
was decided and why, without reading the PRs. This file says when to
write one, what it holds, and how to keep it true. The template is at
the end.

## When to write one

Write or update a design doc before the code when the change:

- adds a feature a user sees, or changes how one behaves;
- adds or changes a gesture or an interaction (drag, swipe, pane
  switching). `docs/lessons.md` ("Design before code") shows what
  happens without one;
- changes something `docs/irreversible.md` lists: a saved format, a
  session id, the wire protocol, identity and access;
- adds a listener, a server or a stack, or removes one.

A bug fix, a refactor that keeps behavior, or a change to one screen's
look does not need one. If a doc covers the area, fix the doc when the
fix changes what it says.

## Before you write

1. Search `docs/` for the topic (`git grep -i <word> origin/main -- docs/`).
   Update a doc that covers it instead of starting a second one.
2. Read the docs it links to, and the code it will change, on
   `origin/main`.
3. If the change is in `docs/irreversible.md`, the doc lists the numbered
   options and the recommendation, and the work waits for the user's
   choice.

## File and title

- One feature, one file: `docs/<topic>.md`, lowercase words joined by
  `-`. Prefix a client: `ios-`, `web-`, `desktop-`.
- The title names the feature or the problem in plain words:
  `# Dragging a pane onto another pane`, not `# DnD v2`.
- Write in English, like the code and the PRs.

## The status line

Line 3 is the status line. It says how much of the doc is real:

| Status | Means |
| --- | --- |
| `Status: proposal.` | Not accepted. No code yet. |
| `Status: accepted. Not implemented.` | The design is settled. Code may start. |
| `Status: phase 1 built (#NNN); phases 2 and 3 not built.` | Part is in `main`. Name the PRs. |
| `Status: built (#NNN).` | All of it is in `main`. |
| `Status: built; <check> is open.` | Built, with a check still to do. |
| `Status: replaced by [x.md](x.md).` | Kept as history. Say what took its place. |

The PR that changes what is built changes this line in the same PR.
A line that says "not implemented" over shipped code misleads the next
reader more than no doc.

## Sections

Use the sections below, in this order. Leave out a section that has
nothing to say; do not leave it empty.

| Section | Required | What it holds |
| --- | --- | --- |
| Problem | yes | What the user or the code cannot do today, with the facts: what happens now, where in the code. |
| Goal | if not obvious | What is true when the work is done, in terms someone can check. |
| Non-goals | when scope is in doubt | What this work does not do, and where it goes instead. |
| Design (or Behavior) | yes | What the user sees and does first, then the model, then the code: state, files, protocol. |
| Alternatives | when there were real choices | Each option and why it lost, in a line or two. |
| Security | if it adds a listener, a credential or trust | Who can reach what, and what stops everyone else. |
| Migration | if it changes saved data or a protocol | What old builds and old data do, and the upgrade path. |
| Tests | yes | How each behavior is checked: unit test, `ui.NewTester`, screenshot, end to end. Name the `docs/invariants.md` items it touches. |
| Phases | if it ships in more than one PR | One line per phase, with its PR once merged. |
| Open questions | while there are any | Each question, the options, and who decides. Empty this section before `accepted`. |

## Rules

- **Behavior before code.** Describe what the user sees, presses and
  gets before the types and the files. A reviewer must be able to judge
  the behavior without reading Go or Swift.
- **Decide, do not survey.** The design section states one design.
  Options go to Alternatives or Open questions.
- **Name things once.** Pick one word for each thing and keep it. Give
  the code name in backticks the first time (`SpaceManifest`).
- **Point at code by symbol and file**, not by line number: line
  numbers go stale in a week. `internal/rex/journal.go`, `paintFrost`.
- **Use a table** to compare ways, states or platforms. Use a numbered
  list for steps in order.
- **Show limits.** Say what the design does not handle and what it costs
  (size, latency, a new dependency).
- **No private data.** The repo is public. Use placeholders such as
  `alice-laptop` and `192.0.2.10`; no real names, host names, paths,
  addresses or keys. Pictures follow the screenshot rules in `CLAUDE.md`.
- **Short.** Most docs fit in 100 to 300 lines. A longer one is usually
  two features: split it, and link them.

## Keeping it true

- Ship the doc with the code, in one PR. A design-only PR is fine for a
  `proposal`; the code PR then updates the status line.
- When the code turns out different from the doc, change the doc in the
  same PR, or add a section that lists the differences and why.
- When a later design replaces this one, set `Status: replaced by ...`
  and keep the old text only if it explains a choice still in the code.
  Otherwise delete the doc.
- When the feature is removed, remove its doc in the same PR.

## Template

```markdown
# <Feature or problem, in plain words>

Status: proposal.

<One paragraph: what this doc decides, and which docs it builds on.>

## Problem

<What happens today, and why it is not good enough. Facts, with paths.>

## Goal

- <A result someone can check.>

## Non-goals

- <What this does not do, and where it goes instead.>

## Design

### Behavior

<What the user sees and does, step by step.>

### Model

<The state and the rules that produce the behavior.>

### Code

<Files, types, protocol messages. Symbols, not line numbers.>

## Alternatives

- **<Option>**: <why it lost>.

## Security

<Who can reach what, and what stops everyone else.>

## Migration

<What old builds and old data do.>

## Tests

- <Behavior> — <how it is checked>.

## Phases

1. <Phase> — <PR, once merged>.

## Open questions

1. <Question> — <options; who decides>.
```
