---
name: IOS-018
description: "Rex Go draws a typed character under the cursor at once, before the host answers, as a marked guess that real output replaces. Use when you change the terminal's input, output or rendering, or its settings."
metadata:
  created: "2026-11-21"
  updated: "2026-11-21"
---

# IOS-018: Predictive echo

Status: built (#960, #962).

## Problem

A key shows on the screen only after a round trip to the host. `TerminalSession.type` queues the bytes and sends them one `write` call at a time on the control connection. The program echoes them and the answer comes back on the data stream. On a link that has a high delay, such as the cellular link whose diagnostics measured a 50 to 600 ms ping, typing feels slow. The `waitingToSend` note tells the user only that the input waits.

## Goal

A printable key shows its character under the cursor at once. The character is marked as a guess. The real output replaces it. A wrong guess never stays. A row in Settings › Terminal turns the feature off.

## Non-goals

- No change to the server or to the wire protocol. The design uses the `write` call and the data stream as they are.
- Not a port of mosh. There is no UDP transport, no state synchronization and no roaming. The control connection stays TCP.
- No change to the desktop app or the web client. They do not have this delay profile.
- Phase 1 predicts printable characters, Return and Delete only. Control keys, arrow keys, function keys, pastes and the keys a full-screen program reads end open guesses instead.

## Design

### What the user sees

With the feature on, a typed character appears at the cursor at once. It is drawn underlined until the real output covers it, as mosh draws its guesses. When the guess was wrong, the underlined cell disappears on its own. The user never edits it.

Settings › Terminal gains a "Predictive Echo" toggle, on by default. With it off, typing looks as it does today.

### The model

A new `Predictor` type, one for each `TerminalView`, holds open guesses. A guess is a glyph at a cell position with a deadline. The view owns it because the view owns the facts a guess needs: the surface's cursor and cell size, the screen mode and the text of the cursor's line.

`TerminalView`'s input path gives the encoded bytes to the predictor before `TerminalSession.type` queues them. The predictor reads its own keys:

- A printable character predicts its glyph at the cursor, and the cursor moves right, with a wrap at the edge.
- Return predicts a new line.
- Delete predicts the removal of the glyph before the cursor.
- Anything else closes the open guesses at once: an escape sequence, a control key, a function key, a paste, or a key while the pane is on the alternate screen.

A guess ends in one of three ways:

1. Output written to its cell shows it or covers it. Each `write` to the surface starts a short wait, then the view compares the guesses with the rows and the cursor the surface reports: a guess whose cell shows its glyph or another one ends.
2. Its deadline passes. The deadline is two round-trip times of the host, at least half a second and at most five seconds.
3. The cursor retreats or runs past where the guesses put it, the surface closes or its grid remaps on a resize. The predictor clears all guesses. A reconnect's writes and cursor moves end them the same way.

The overlay draws over the terminal's surface. It never feeds bytes into the emulator, so a wrong guess can never corrupt the terminal's state.

### What stops a bad guess

The predictor stays on the safe side:

- It predicts only on the main screen, not on the alternate screen, where a full-screen program answers keys with redraws.
- It does not predict while a bracketed paste runs.
- It does not predict while the line at the cursor shows a password prompt. The check looks for `password`, `passphrase` or `PIN` in the line, letter case ignored.

### The code

- `ios/GoRex/Terminal/Predictor.swift`: the guesses, the keys it reads, the deadlines and the resolve rules.
- `ios/GoRex/Terminal/PredictionOverlay.swift`: the view that draws underlined cells over the surface.
- `ios/GoRex/Terminal/TerminalView.swift`: the input path gives the predictor the bytes, each output `write` schedules a resolve, and `close` and `resized` clear the guesses.
- `ios/GoRex/Views/SettingsView.swift`: the toggle, a new `UserDefaults` key `predictiveEcho`, true by default.

## Alternatives

- A shadow emulator, a second Ghostty run that predicts the program's own answers, would guess in editors too. It doubles the emulator and makes the reconcile much harder. Phase 1 stays at the safe keys.
- Full mosh, with UDP and state synchronization, removes the connection instead of its delay. It needs a new transport and a UDP bridge for the tailcat proxy path, while the control connection stays TCP. Not taken.
- No toggle. The switch is the escape hatch for a wrong guess the guards miss, and it is cheap. The requirement asks for it.

## Security

A secret typed at a prompt the password check misses can show as an underlined guess until it clears. The guess stays an overlay cell. It never reaches the emulator, the server or a log. Nothing else changes: no new listener, no credential, no wire change.

## Tests

- `PredictorTests`: type predicts cells, output covers and clears, a deadline clears, a reconnect clears, and an unpredicted key closes the open guesses.
- A simulator screenshot shows an underlined guess before the answer arrives. `docs/invariants.md` has no item for the terminal's echo.
- The toggle's test: off, `type` makes no guess.

## Phases

1. This doc.
2. The predictor, the overlay and the toggle, in one PR.

## Open questions

- Does the deadline use the control ping's round trip, or a measure of write-to-output delay the session keeps? The author decides in phase 2.
