---
name: REF-015
description: "How CI picks its jobs and checks to stay inside the InstaBox sandbox allowance, and how the weekly CI health report tracks it. Use when you change a workflow in .github/workflows, tools/ciplan or tools/cistats, or when the CI health issue shows a flag."
metadata:
  created: "2026-11-28"
  updated: "2026-11-28"
---

# REF-015: Continuous integration

Status: reference. It describes the code in main.

This doc describes how CI decides what to run, why it runs so little, and how the weekly report tells us when it grows again. The source of truth is the code: `.github/workflows/ci.yml`, `tools/ciplan/plan.go` and `tools/cistats`.

## Why

CI runs on InstaBox sandboxes. The InstaBox Pro plan allows 500 sandbox starts a day. The count resets at 00:00 UTC. The plan also allows 200 starts an hour and 30 starts a minute. A start above a limit is blocked by default. If we enable extra starts, each one costs $0.05.

Every job that runs on an `instabox-*` runner is one start. A job that is skipped starts nothing. The size of the job does not matter. A 20-second job costs the same as a 7-minute job.

We measured the baseline from 2026-11-21 to 2026-11-27 with the GitHub API. CI started 892 to 1068 sandboxes a day. A code PR's CI run started 8 sandboxes. A push to main started 9. The slowest job took about 7 minutes. The work was spread over many small jobs, so the start count was high and the wall time was not.

The target is far below 500 a day, so that a busy day, a burst of merges or a new workflow does not block CI.

## Jobs

Every workflow in `.github/workflows` is in this table. "instabox" means one sandbox start per run of the job. A job on a self-hosted runner uses no start.

| Workflow | Job | Runner | Trigger |
| --- | --- | --- | --- |
| `ci.yml` | `linux` | `instabox-large` | Pull request, push to main, manual. Docs-only changes start no run (paths filter). |
| `ci.yml` | `test-windows` | self-hosted Windows runner | The same as `linux`. Pull requests from forks do not run it. |
| `pr-verified.yml` | `verified` | `instabox-medium` | `pull_request_target`: opened, edited, synchronize, reopened. |
| `maintenance.yml` | `audit` | `instabox-medium` | Monday 03:17 UTC, manual. It also posts the CI health report. |
| `web.yml` | `deploy` | `instabox-medium` | Push to main that changes `web/**`, `assets/**`, `go.mod`, `go.sum` or the workflow. Manual. |
| `testflight.yml` | `guard` | `instabox-medium` | Push to main that changes `ios/**`, `go.mod`, `go.sum` or `internal/tailnode/**`. Manual. |
| `testflight.yml` | `upload` | self-hosted Mac runner (`gorex-testflight`) | The same as `guard`. |
| `release.yml` | `guard` | `instabox-medium` | Manual, on main. |
| `release.yml` | `draft` | self-hosted Mac runner, or the Windows runner for a Windows-only release | Manual, on main. |
| `release.yml` | `unix` | self-hosted Mac runner | Manual, on main. |
| `release.yml` | `windows` | self-hosted Windows runner | Manual, on main. |
| `release.yml` | `checksums` | `instabox-medium` | On main, when at least one of `unix` and `windows` succeeded and none failed or was cancelled. A skipped one does not block it. |
| `release.yml` | `web-r2` | `instabox-medium` | Manual, on main, target `all` or `web`. |
| `ios-test.yml` | `test` | self-hosted Mac runner (`gorex-testflight`) | Manual. |
| `delete-shots.yml` | `delete` | self-hosted Mac runner | A merged pull request. |

### The one-job rule

`ci.yml` has one instabox job, `linux`. It runs on one `instabox-large` machine (8 vCPU, 16 GiB). All Linux checks are steps of this job: plan, what-to-test, gofmt, vet, build, lint, web test, test and the R2 upload. A step that does not apply is skipped inside the job, and it costs no start.

Add a step to `linux`. Do not add a job. A new instabox job names its expected starts a day in the PR. See [A new job on instabox](lessons.md#a-new-job-on-instabox).

`plan` has no condition. The `what-to-test` step has only `github.event_name == 'pull_request'`. The checks after it have the `plan` output in their condition and, when they must run after a failure, `!cancelled()`. Read `ci.yml` for each condition. One failure does not hide the other failures, and each failed step fails the job.

The Windows tests run on a self-hosted Windows runner. They use no start. The Mac mini's runners build iOS and releases, and no CI job waits for them.

## What runs for a change

`tools/ciplan` decides. The `plan` step of `linux` and of `test-windows` runs `go run ./tools/ciplan`. The tool writes `key=value` lines to `$GITHUB_OUTPUT`, and the later steps read them. The tool never skips a test that the change can break. In case of doubt, it runs everything.

### Inputs

- `BASE` is the commit to compare with. It is the base of the pull request, or the commit before the push.
- `GITHUB_EVENT_NAME` tells the tool about a manual run.
- The tool runs `git diff --name-only BASE HEAD` to get the changed files.
- The tool reads the package graph of the module with `go/build` for linux, windows and darwin. It reads import lines only. It needs no module download and runs before the Go caches are restored.

### Rules

From the changed files, `plan` in `tools/ciplan/plan.go` builds the sets below. Read that file for the exact tables. Do not copy them to a doc, because a copy drifts.

- **Seeds.** A changed `.go` file seeds its package. A changed file of another kind seeds the nearest package above it. A file that a `//go:embed` pattern names seeds the package that embeds it.
- **`readsElsewhere`.** A table of files that a test or a generator reads outside its own package. The graph cannot show these reads. A change to such a file seeds the packages in the table.
- **`ruleTests`.** Tests of the root package that read files across the repo, such as `TestNoExcuseComments`. When only a file that they read changes, the root package runs only these tests (`root_run`). Otherwise the root package runs in full.
- **Reverse-dependency closure.** The tests run for each seed and for every package that imports a seed, directly or not. They also run for each package whose tests import one of these.
- **Per system.** A file name suffix, such as `_windows.go` or `_darwin_test.go`, limits the change to that GOOS. Any other file counts for all systems, because a `//go:build` line can be the thing that the change edits. The Linux job runs the tests of the Linux packages. The Windows job runs the Windows packages, but never the root package. Darwin gets vet only.
- **vet and lint.** vet runs per GOOS on the packages whose `.go` files changed and on their importers. lint runs on the same packages as the Linux vet.
- **gofmt.** It checks the changed `.go` files that still exist.
- **build.** The cross-build for four targets runs when any `.go` file changed, when an embed that is not only for tests changed, or when a `readsElsewhere` entry marks it.
- **web.** A change under `web/` that is not a `.go` file runs the web typecheck and the web tests.
- **web-r2.** A change under `web/` or `assets/` builds and uploads the web client to R2, on a push to main only.

### Everything runs

These cases run all the checks:

- A manual run (`workflow_dispatch`).
- A run with no usable base: an empty base, an all-zero base (a new branch), or a base that is not an ancestor of `HEAD` (a force-push).
- An empty diff.
- A change to `go.mod`, `go.sum`, a `.golangci*` file, `.github/workflows/ci.yml` or `tools/ciplan/`.

### Failure modes

- A package that does not parse makes the graph fail. The `plan` step fails with the error, and the job is red. It does not plan from a partial graph.
- A test that reads a file outside its package, and is not in `readsElsewhere` or `ruleTests`, is skipped when only that file changes. This is the main way the plan can miss a failure. See [A test that reads a file outside its package](lessons.md#a-test-that-reads-a-file-outside-its-package).
- The tests of `tools/ciplan` check its graph against `go list` on the repo. Any Go change runs them.

### Examples

| Change | What runs |
| --- | --- |
| iOS only (#1247) | `TestNoExcuseComments` in the root package, because it reads `.swift` files. The what-to-test step also runs on every pull request. |
| Docs only (#1233) | Nothing. The paths filter starts no run. |
| `internal/rex` (#1212) | vet and lint on the package and its importers, the cross-build, the root package, and 10 other packages. |
| `web/src` (#1058) | The web typecheck and tests, and `TestNoExcuseComments`. On a push to main, `web-r2` also runs. |
| Docs, `tools/cistats` and `maintenance.yml` (#1267) | vet and lint on `./tools/cistats`, tests of `./tools/ciplan` and `./tools/cistats`, and `TestNoExcuseComments` and `TestWorkflowsTargetFork` in the root package. No web. `linux` took 4 minutes (237 s) and `test-windows` 32 s. A full run takes 15 to 27 minutes. |

## Rules for authors

- Add steps to `linux`. Do not add jobs. Do not widen a trigger of an instabox workflow without a reason in the PR.
- If a test or generator reads a repo path outside its package, add the path to `readsElsewhere` in `tools/ciplan/plan.go`. If a root test reads files across the repo, add it to `ruleTests`.
- Run the Go tests one after the other. A run of the root package at the same time as the `-race` run of the other packages made timing tests fail (`TestDataSlowReader`).
- Do not add a retry. A failed test fails the step. Fix the test. See [CI retry](lessons.md#ci-retry).
- A change to `tools/ciplan` or `ci.yml` runs everything. Check the plan with `BASE=origin/main go run ./tools/ciplan` before the push.

## Tracking

`tools/cistats` reads the GitHub REST API with `GITHUB_TOKEN` or `GH_TOKEN`. It uses the standard library only. Run it by hand with:

```sh
go run ./tools/cistats -days 7 -repo si-mods/gorex
```

The `audit` job of `maintenance.yml` runs it every Monday after the audit, with `-days 7`. The window is the 7 whole UTC days before today, today excluded. A Monday run covers Monday to Sunday of the week before, and every count is final. This adds no start, because the job already runs. If the repository variable `CI_HEALTH_ISSUE` holds an issue number, the report is posted as a comment on that issue. If not, the report is in the job log. The step runs even when the audit fails.

The report is in Markdown and has these parts:

- **Flags.** The thresholds that the window crossed, or `none`.
- **Starts per day** in UTC, with the maximum and the average, and the busiest hour and minute, shown against the limits of 200 and 30.
- **Starts per workflow and job**, the top 10.
- **Duration of the CI `linux` job** (p50, p90, maximum), for each event: pull request, push and manual run.
- **Steps of the CI `linux` job**: how often each step ran and how often it was skipped. This shows how selective `tools/ciplan` is.
- **Failure rate** of the `linux` job.

A start is a job with an `instabox*` label that was not skipped and got a runner. The tool counts starts by the `started_at` time of the job. It lists runs one day at a time, starting one day before the window, because a run that was created before the window can start jobs in it. The API returns at most 1000 results for a query. If a day returns 1000, the tool lists that day again as two 12-hour ranges. If a half is still full, the tool stops with an error that names the day. Jobs are unique by id, because a re-run lists a job once for each attempt.

A week holds about 1900 runs, and each run needs one jobs call. `GITHUB_TOKEN` allows 1000 requests an hour. The tool reads `X-RateLimit-Remaining` and `X-RateLimit-Reset` on every answer. With fewer than 20 requests left, it logs `rate limit: waiting until <time>` and sleeps until the reset time plus 5 seconds. One wait is at most 65 minutes; a longer wait is an error. A 403 or 429 answer with no requests left waits once and sends the request again. Any other 403 stops the tool with an error. The `audit` job has `timeout-minutes: 90` so that a full wait fits. Workflow and job names in the report are in backticks, so a name cannot make a mention or break a table.

| Flag | Threshold | Allowance |
| --- | --- | --- |
| Starts in one UTC day | more than 350 | 500 |
| Starts in one hour | more than 150 | 200 |
| Starts in one minute | more than 25 | 30 |
| CI `linux` p90 duration | more than 20 minutes | none |

When a flag trips:

1. Read the per-job table. Find the workflow or job that grew.
2. Check for a new job, a new matrix entry or a wider trigger. Look at the diff of `.github/workflows` since the last good report.
3. If the `linux` steps are rarely skipped, `tools/ciplan` runs too much. Check which "everything" trigger fires. Look at manual runs, new branches, force-pushes and changes to `go.mod`. Revisit the list in `forceAll`.
4. If only the duration grew, find the slow step. Do not add jobs to make it faster.
5. Fix the cause in a PR. If the cause is a lesson, add an item to `docs/lessons.md`.

`docs/maintenance.md` tells the maintainer to read the CI health issue each month.

## History

- #1246 folded the CI jobs into one instabox job, `linux`. The `pr-verified` workflow stayed as it was after review.
- #1256 added `tools/ciplan`. CI now runs only the packages and checks that a change reaches.
- This doc and `tools/cistats` add the weekly report.

## Alternatives

- **Drop `synchronize` from `pr-verified`.** It would save one start for each push to a pull request. It lost, because the check attaches to the head SHA. After a push, the new head has no check, and an earlier failure no longer shows.
- **Run the root package tests and the other package tests at the same time.** It would save wall time. It lost, because the CPU load made timing tests fail.
- **Retry a failed test.** It lost. A retry hides a real flake. See [CI retry](lessons.md#ci-retry).
