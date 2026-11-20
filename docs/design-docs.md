# Writing a design doc

Status: standing rule.

A design doc records what a feature does before someone writes the code. The next author and the reviewer read it. It tells them what the team decided and why, so they do not need to read the PRs. This file tells you when to write a design doc, what it contains, and how to keep it true. The template is at the end.

## When to write one

Write or update a design doc before the code when the change does one of these things:

- It adds a feature that a user sees, or it changes how a feature behaves.
- It adds or changes a gesture or an interaction, such as drag, swipe or pane switching. The "Design before code" section of `docs/lessons.md` shows what happens without a doc.
- It changes an item in `docs/irreversible.md`: a saved format, a session id, the wire protocol, or identity and access.
- It adds or removes a listener, a server or a stack.

A bug fix does not need a design doc. A refactor that keeps the behavior does not need one. A change to the look of one screen does not need one. If a doc covers the area and the change makes the doc wrong, update the doc.

## Before you write

1. Search `docs/` for the topic: `git grep -i <word> origin/main -- docs/`.
2. If a doc covers the topic, update that doc. Do not start a second doc.
3. Read the linked docs on `origin/main`.
4. Read the code that the change touches on `origin/main`.
5. If the change is in `docs/irreversible.md`, write the numbered options and your recommendation in the doc. Wait for the user to choose.

## Language and line breaks

- Write in English, in Simplified Technical English (ASD-STE100). Use the [asd-ste100 skill](https://github.com/danyuchn/asd-ste100-skill/blob/master/SKILL.md) in its STE-flavored mode.
- Apply the structural rules of the skill. Use active voice and simple tenses. Use no phrasal verbs and no semicolons. Use one idea in each sentence.
- Keep instructions to 20 words or fewer. Keep descriptions to 25 words or fewer.
- Use one name for one thing in the whole doc. Write the code name in backticks the first time, for example `SpaceManifest`.
- Check the draft with `scripts/ste-lint.py` from the skill if you have the skill installed.
- Do not wrap lines by hand. Put each paragraph, list item and table row on one line. The editor wraps the text for the reader.

## File and title

- Use one file for one feature: `docs/<topic>.md`. Write the topic in lowercase words with `-` between them.
- Start the name with the client when one client owns the feature: `ios-`, `web-` or `desktop-`.
- The title names the feature or the problem in plain words. Write `# Dragging a pane onto another pane`, not `# DnD v2`.

## The status line

Line 3 is the status line. It tells the reader how much of the doc is in `main`.

| Status | Meaning |
| --- | --- |
| `Status: proposal.` | Nobody accepted the design. No code exists. |
| `Status: accepted. Not implemented.` | The design is final. Code work can start. |
| `Status: phase 1 built (#NNN). Phases 2 and 3 not built.` | Part of the design is in `main`. Name the PRs. |
| `Status: built (#NNN).` | All of the design is in `main`. |
| `Status: built; <check> is open.` | The code is in `main`, but a check is still open. |
| `Status: replaced by [x.md](x.md).` | The doc is history. Name the doc that replaces it. |

The PR that changes what `main` contains must change the status line too. A doc that says "not implemented" about code in `main` misleads the reader. No doc is better than that.

## Sections

Use the sections below, in this order. If a section has no content, remove it. Do not leave an empty section.

| Section | When | Content |
| --- | --- | --- |
| Problem | always | What the user or the code cannot do now. Give the facts: what happens now, and where in the code. |
| Goal | when the goal is not clear from the problem | What is true after the work ends. Write results that someone can check. |
| Non-goals | when the scope is not clear | What this work does not do, and where that work goes. |
| Design (or Behavior) | always | First what the user sees and does. Then the model. Then the code: state, files and protocol. |
| Alternatives | when real choices existed | Each option and the reason it lost, in one or two lines. |
| Security | when the change adds a listener, a credential or trust | Who can reach what, and what stops all other people. |
| Migration | when the change touches saved data or a protocol | What old builds and old data do, and how they upgrade. |
| Tests | always | How you check each behavior: unit test, `ui.NewTester`, screenshot or end-to-end run. Name the `docs/invariants.md` items that the change touches. |
| Phases | when the work needs more than one PR | One line for each phase. Add the PR number after the merge. |
| Open questions | while questions exist | Each question, its options, and who decides. Answer all questions before the status changes to `accepted`. |

## Rules for the content

- **Write the behavior before the code.** Describe what the user sees, presses and gets. Then describe the types and the files. A reviewer must understand the behavior without the Go or Swift code.
- **Give one design.** The Design section states one design. Put the other options in Alternatives or in Open questions.
- **Point at code by symbol and file.** Do not use line numbers, because they change often. Write `internal/rex/journal.go` or `paintFrost`.
- **Use a table to compare** ways, states or platforms. Use a numbered list for steps in sequence.
- **State the limits.** Write what the design does not handle. Write what it costs: size, latency or a new dependency.
- **Keep private data out.** The repo is public. Use placeholders such as `alice-laptop` and `192.0.2.10`. Do not write real names, host names, user paths, addresses or keys. For pictures, follow the screenshot rules in `CLAUDE.md`.
- **Keep the doc short.** Most docs need 100 to 300 lines. A longer doc usually describes two features. Split it into two docs, and link them.

## Keeping the doc true

- Put the doc and the code in one PR. A PR with only the doc is acceptable for a `proposal`. The code PR then updates the status line.
- If the code is different from the doc, change the doc in the same PR. As an alternative, add a section that lists each difference and its reason.
- If a new design replaces the doc, set `Status: replaced by ...`. Keep the old text only if it explains a choice that the code still uses. If not, remove the doc.
- If a PR removes the feature, the same PR removes the doc.

## Template

```markdown
# <Feature or problem, in plain words>

Status: proposal.

<One paragraph: what this doc decides, and which docs it extends.>

## Problem

<What happens now, and why it is not sufficient. Facts, with paths.>

## Goal

- <A result that someone can check.>

## Non-goals

- <What this work does not do, and where that work goes.>

## Design

### Behavior

<What the user sees and does, step by step.>

### Model

<The state and the rules that make the behavior.>

### Code

<Files, types and protocol messages. Use symbols, not line numbers.>

## Alternatives

- **<Option>**: <the reason it lost>.

## Security

<Who can reach what, and what stops all other people.>

## Migration

<What old builds and old data do.>

## Tests

- <Behavior>: <how you check it>.

## Phases

1. <Phase>: <PR, after the merge>.

## Open questions

1. <Question>: <options, and who decides>.
```
