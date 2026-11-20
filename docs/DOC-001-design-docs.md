---
name: DOC-001
description: "Rules and a template for design docs: when to write one, ids and categories, front matter, sections and conflicts. Use when you write, rename or review a design doc."
metadata:
  created: "2026-11-20"
  updated: "2026-11-20"
---

# DOC-001: Writing a design doc

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

1. Search `docs/` for the topic: `git grep -i <word> origin/main -- docs/`. The file names start with the category, so `ls docs/IOS-*` lists the iOS docs.
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

## Id and file name

Each design doc has an id: a category code, a `-`, and a number. An example is `IOS-001`.

- The file name is the id, a `-`, and a short topic in lowercase words with `-` between them: `docs/IOS-001-tailnet.md`. The category is in the id, so do not repeat it in the topic.
- The number counts the docs in the category, from `001`. Give a new doc the next free number. Use 3 digits. After `999`, use 4 digits. Do not rename the older docs.
- An id never changes and nobody uses it again. If the scope of a doc moves to another category, keep the id and change the text.
- When a doc refers to another doc, write the id and link the file: [`NET-002`](NET-002-host-links.md).

## Categories

| Code | Category | Scope |
| --- | --- | --- |
| `DOC` | Docs about docs | Rules for the docs in `docs/` |
| `DSK` | Desktop app | Windows, menus, panes and the app server on the desktop |
| `IOS` | iOS (Rex Go) | The iPhone and iPad app, its widgets and its Live Activities |
| `WEB` | Web client | The web client in `web/` and its gateway paths |
| `NET` | Connection and identity | Pairing, device keys, tailnet, relays and login |
| `MOD` | Model and protocol | The layout model, projects, the Rex API and OSC sequences |
| `REF` | Reference | Docs that describe how a part of GoRex works now. They have no design to build. Examples are the CLI, the Lua config and the session server. |

- Put a doc in the category of the part that owns most of it. A doc for all clients goes in the category of the shared model or connection.
- Keep the categories few. Add one only when no category fits.
- A new category is a new row in this table. Add the row in the PR that adds the first doc of the category.
- A code has 2 to 4 capital letters. A code never changes and nobody uses it again. If no new docs go in a category, write "No new docs." in its Scope and keep the row.
- `go run ./tools/verify` checks the file name, the category, that each id is unique, the front matter and the title.

## Front matter and title

A design doc starts with YAML front matter, then the title, then the status line:

```markdown
---
name: IOS-001
description: "Rex Go joins a tailnet inside the app, with no Tailscale app and no system VPN. Use when you change the iOS tailnet engine or its settings."
metadata:
  created: "2026-11-09"
  updated: "2026-11-20"
---

# IOS-001: Rex Go on the tailnet

Status: built (#369).
```

- The front matter follows the [Agent Skills](https://agentskills.io/specification) format. It has the keys `name`, `description` and `metadata`, in this order, and no other keys.
- `name` is the id of the doc, 1 to 64 characters. It is the same as the start of the file name.
- `description` is one or two sentences in double quotes, 1 to 1024 characters. The first sentence says what the doc decides. The second starts with "Use when" and says when to read it. An agent reads only the descriptions to find the doc for a task, so name the feature and the code area in plain words.
- `metadata` has two keys, `created` and `updated`, in this order. Each is a date in the form `YYYY-MM-DD`, in double quotes. `created` is the day of the first commit of the doc. It never changes.
- Every PR that changes a design doc sets `updated` to the day of its last change to the doc. This includes a change of one word. `go run ./tools/verify` refuses a doc that the branch changes when its `updated` date is earlier than the change.
- The title line comes after the front matter and one blank line. It is the id, a `:`, and the title. The title names the feature or the problem in plain words. Write `Dragging a pane onto another pane`, not `DnD v2`.
- The status line comes after the title and one blank line. It starts with `Status: `. See the next section.

## The status line

The status line tells the reader how much of the doc is in `main`.

| Status line | Meaning |
| --- | --- |
| `Status: proposal.` | Nobody accepted the design. No code exists. |
| `Status: accepted. Not implemented.` | The design is final. Code work can start. |
| `Status: phase 1 built (#NNN). Phases 2 and 3 not built.` | Part of the design is in `main`. Name the PRs. |
| `Status: built (#NNN).` | All of the design is in `main`. |
| `Status: built (#NNN). <check> is open.` | The code is in `main`, but a check is still open. |
| `Status: replaced by [IOS-012](IOS-012-x.md).` | The doc is history. Name the doc that replaces it. |
| `Status: reference. It describes the code in main.` | A `REF` doc. It describes the code and has no phases. A PR that changes the behavior that it describes updates it. |

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
- **Keep the doc short.** Keep the body under 500 lines. Most docs need 100 to 300 lines. A longer doc usually describes two features. Split it into two docs, and link them.

## Keeping the doc true

- Put the doc and the code in one PR. A PR with only the doc is acceptable for a `proposal`. The code PR then updates the status line.
- If the code is different from the doc, change the doc in the same PR. As an alternative, add a section that lists each difference and its reason.
- If a new design replaces the doc, set `Status: replaced by ...`. Keep the old text only if it explains a choice that the code still uses. If not, remove the doc.
- If a PR removes the feature, the same PR removes the doc. Nobody uses its id again.

## When docs disagree

An id is a name. A smaller id does not win over a larger one, and a newer doc does not win over an older one.

- **The code decides.** If two docs disagree about built behavior, read the code. Change the doc that the code contradicts, in the PR that finds the conflict.
- **A replacement says so.** A doc that replaces another doc, or one section of it, writes `Replaces: [NET-002](NET-002-host-links.md), section "Pairing".` in its first paragraph. The same PR changes the status line or the section of the old doc to name the new doc.
- **A person decides the rest.** If two docs disagree about behavior that is not built, the PR that finds the conflict stops. The author writes the options and a recommendation, and the user chooses. Then one of the docs gets the `Replaces:` note.
- A conflict is a defect. Do not leave two docs that disagree.

## Template

```markdown
---
name: <CODE>-<NNN>
description: "<What this doc decides.> Use when <the task that needs it>."
metadata:
  created: "<YYYY-MM-DD>"
  updated: "<YYYY-MM-DD>"
---

# <CODE>-<NNN>: <Feature or problem, in plain words>

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
