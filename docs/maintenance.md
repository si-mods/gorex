# Maintenance

The checks no pull request triggers, because the rot they find was merged
earlier. Run this monthly, and before a release. Findings go into a pull
request, or into an issue with a reason; a finding that came from a merged
PR also gets an item in `docs/lessons.md`.

## Dead code

```sh
go run ./tools/verify -audit
```

It runs `deadcode` for linux, windows and darwin together, and reports only
what no target can reach in any of them. A function unused on one system is
not reported while another reaches it. `ios/**` and
`web/{tailnet,tailcat,jsbridge,wsclient}/**` are not reported: gomobile and
the wasm engines call them from Swift and JavaScript, which the Go analysis
cannot see.

Delete the function with its tests and the comment that names it, in the
same pull request. Dead code left behind a feature is not "for later": it
is the reason the next reader cannot tell what runs.

`deadcode` exits 0 with findings; the tool decides by the output. A new
gate that cannot fail loudly passes everything.

## Dangerous code

Read the results, not a count. Each hit must have a reason a reviewer can
check:

```sh
rg -n 'net\.Listen|ListenAndServe' --glob '*.go' -g '!*_test.go'
rg -n 'exec\.Command|syscall\.Exec' --glob '*.go' -g '!*_test.go'
rg -n 'InsecureSkipVerify: true' --glob '*.go' -g '!*_test.go'
rg -n 'https?://' --glob '*.go' --glob '*.ts' --glob '*.swift' -g '!*_test.go' -g '!**/node_modules/**'
```

- A listener needs its auth model, local panes, remote hosts and iOS
  together.
- `os/exec` arguments never come from a remote message whole.
- A TLS skip is paired with a check that replaces the chain, or it is on
  loopback. `internal/pairaddr` skips the chain because the pairing code
  proves the machine; the websocket ones skip the Origin check that
  `Handler` already made.
- A URL in the code is the user's own server, or a doc example. The repo
  has no telemetry and no third-party endpoint.

Agent config runs code: a change to `.claude/`, `.agents/`, `.omp/` or
`.github/` is read whole before it is approved.

## References

Every `*.md` a comment names must exist:

```sh
rg -o --no-filename '[A-Za-z0-9_./-]+\.md' --glob '*.go' --glob '*.swift' --glob '*.ts' | sort -u
```

A comment that points at a deleted file teaches the next reader a rule
that has no source. Fix the comment, or restore the file.

## Size

- `state.go` stays under 4000 lines. New behavior goes in a file of its
  own concern. A file that assigns floats, panes, browsers and hosts has
  stopped being one thing.
- Files over 1000 lines are read before they grow: a helper added to one
  is a helper the next PR cannot find.
- When two places apply "the same rule", they call one function. A copy
  drifts; `docs/lessons.md` has the device-path case.

## Disabled features

A flag, an early return or `isEnabled = false` needs a linked issue that
says what must happen before the code comes back or is deleted. With no
issue, delete the code now: git keeps it.

## CI health

`maintenance.yml` posts a weekly report on sandbox starts and CI duration as a
comment on the issue named by the repository variable `CI_HEALTH_ISSUE`. Read
the last report monthly, with the checks above. A flag in it is a defect:
`docs/REF-015-ci.md` says what to do.

## Dependencies

`go.mod` and `web/package.json` changes are read, not counted. A new
dependency needs a line in the PR saying what the standard library or an
existing dependency cannot do.
