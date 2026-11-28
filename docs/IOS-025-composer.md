---
name: IOS-025
description: "Rex Go's Composer Mode is a bar pinned over the key bar that writes a message, attaches photos, camera shots and files, and sends the lot as one prompt: the attachment paths first, then the text and Return. Use when you change the compose bar, its attachments, or the batch upload that carries them."
metadata:
  created: "2026-11-25"
  updated: "2026-11-28"
---

# IOS-025: A composer for messages with attachments

Status: proposal.

This doc adds the composer to Rex Go, as Rex's iOS app has it. A user writes a message, attaches photos, camera shots and files, and sends them to the terminal in one submission. It extends the uploads of Add Photo and Add File (`ios/GoRex/Terminal/TerminalView+Upload.swift`).

## Problem

An agent such as Claude Code or Codex takes a message and the images it talks about in one prompt. On the phone a user cannot do that: Add Photo pastes a path the moment the file is up, before the message is written. The user must send the paths alone, or write the text first and hope the paths land after it. There is also no place to look a message over before it goes: the terminal types every Return.

## Goal

- Settings > Terminal's Composer Mode pins a compose bar over the key bar.
- The bar takes a message, and attaches photos, camera shots and files.
- The mic moves into the bar: typing and dictation land in the message field to read before Send.
- Send uploads the attachments, lets the server type their paths, then pastes the text and presses Return: one submission, paths first.
- A host that takes no uploads (`canUpload` off) still composes text, without attachments.

## Non-goals

- A draft kept between hosts. A bar left on a terminal the user leaves loses its message.
- Sending attachments without Return, or paths the user edits before they go.
- Attachments that already sit on the host. Every attachment uploads at Send.

## Design

### Behavior

- The bar is up while Composer Mode (`composerMode` in user defaults) is on. On iPhone, it replaces the separate bottom key bar and folds terminal quick keys (`Esc`, `Tab`, `Enter`, `Delete`, arrow stick, and `⋯` More Keys) behind a horizontal swipe on the bar or the `+` menu so they unfurl inline across the bar on demand (see [`IOS-030`](IOS-030-composer-centric-bar.md)). Users can toggle Composer Mode from Settings > Terminal, from the Compose button (`square.and.pencil`, `keybar.compose`) in the key bar's left capsule, or dismiss it via `Show Key Bar` in `+` (or `⋯`) or a downward swipe anywhere on the bar (first swipe dismisses the keyboard if up, next swipe exits Composer Mode). Toggling it takes effect immediately on already-open terminals as well as newly opened ones, transferring keyboard focus cleanly between the compose field and the terminal if the keyboard is up.
- It is a compact single-row glass pill (`48 pt` at rest, radius `24 pt`): a circular `+` button on the left, a single-line message field in the middle, and a circular mic button plus circular Send arrow on the right. As soon as text wraps past the single-row slot or contains a newline, the bar reflows into a Grok-style two-row card: the message field spans the full width across the top (growing upward to `160 pt` before scrolling smoothly with automatic caret tracking) while `+`, the mic, and prominent accent Send sit in a dedicated `36 pt` action row below. Clearing or deleting back to a short single line collapses the card back to the `48 pt` pill. Showing, growing, or hiding the bar slides the terminal up and down without resizing the terminal grid, keeping the program's own input row uncovered while avoiding PTY resizes on toggle. The keyboard stays in view and the field takes its focus.
- The field grows with its lines up to a cap, then scrolls. Its placeholder names the block the message goes to — "Message to Claude Code" — as Rex's does, so a pane is never guessed.
- The mode moves the mic and voice dictation inline: the key bar's right capsule is the stick and the keyboard alone, and the bar holds the screen's own mic beside Send. While dictating, the bottom `36 pt` row cross-fades inline into a single-row voice view (keeping the `48 pt` height at rest and keeping any multi-line draft visible above it): a circular Cancel button on the left (discards the dictation), a live waveform and scrolling transcript in the middle, and a circular accent Done button (checkmark; lands the text in the field for editing) on the right. Dictation never sends: the user reads the text and taps Send. Pressing and holding anywhere on the compose bar (outside `+`, Send, quick keys, attachments, or existing text in the field) also starts push-to-talk dictation and converts to text into the field on release.
- The attach button, a plus, opens a menu of Paste (into the message field) plus Photo, Camera (where available) and File (when the host takes uploads), followed on iPhone by an inline terminal section with `More Keys` (unfurls the inline quick-keys strip), `Hide Keyboard` (while the keyboard is up), and `Show Key Bar`. Because Paste and uploads live in `+`, the key bar hides the redundant `⌘` key in Composer Mode while keeping the Compose toggle button highlighted in the left capsule.
- Each attachment is a thumbnail in a strip over the input row: the image itself, or a document mark. A tap on its mark removes it.
- Send stays off while the text is empty and no attachment is staged. While the files go up, Send spins and the attach menu holds still.
- The attachments go up as one batch. When every file has arrived, the server types their paths into the terminal, as it does for Add Photo. Then the app pastes the text and sends Return, through the same input path a paste and the Enter key take.
- A failure keeps the message and says why, so the user can try again. A send that goes through empties the field, the strip and the staging folder for the next message.

#### Swipe to switch

- On iPhone (`UIDevice.current.userInterfaceIdiom != .pad`), `ComposerBar` registers left and right `UISwipeGestureRecognizer` instances with `cancelsTouchesInView = false` so users reach terminal keys without opening the `+` menu.
- Swiping left while quick keys are collapsed calls `setQuickKeysExpanded(true, animated: true)` and plays light impact feedback (`UIImpactFeedbackGenerator(style: .light)`).
- Swiping right while quick keys are expanded calls `setQuickKeysExpanded(false, animated: true)`, plays light impact feedback, and restores first-responder focus to `field`.
- Gesture priority: `gestureRecognizerShouldBegin(_:)` refuses horizontal swipes while `voiceActive`, `sending`, or `holdingVoice` is true, while `dismissPan` or `stickDrag` is in `.began` or `.changed`, when the touch starts on `stickBtn` while quick keys are open, or when the touch starts inside `field` during multi-line editing (`multiLine && field.isFirstResponder`) with a non-empty selection (`field.selectedRange.length > 0`). Horizontal swipes across the single-row pill pass through, and `gestureRecognizer(_:shouldBeRequiredToFailBy:)` preempts ancestor navigation-pop pans so swiping right on `ComposerBar` collapses quick keys instead of popping the terminal screen.
- Accessibility and motion: VoiceOver keeps the `More Keys` action in `accessibilityCustomActions` alongside `+` › `More Keys` and the `✕` collapse button. Under Reduce Motion, `setQuickKeysExpanded` uses its existing 150 ms cross-fade with no spring scale.

### Model

- The bar is `ComposerBar`, a view of the terminal's host view: the text field, the staged `Attachment`s, and whether a send is running.
- An `Attachment` is a file staged in a folder of the temporary directory, with a thumbnail for the strip. Photos are staged as their original files, as Add Photo's are. A camera shot is written as a JPEG. A file is moved in from the picker's copy.
- A send empties the folder, which the next attachment makes again.

### Code

- `ios/GoRex/Terminal/Composer.swift`: `ComposerBar` and its `modeKey`. The camera's and the document picker's delegate objects live here too.
- `ios/GoRex/Terminal/TerminalView.swift`: `TerminalHost` opens the pinned bar when `composerMode` is on and hands the screen's mic its `micHolder` (`micTarget`); the `KeyBar` drops its own mic into the bar then. `dictationInsert` routes a finished dictation to the field, and the `becomeFirstResponder`/`resignFirstResponder` overrides give it the terminal's keyboard, so the key bar's keyboard key and a pane switch still work.
- `ios/GoRex/Terminal/TerminalView+Upload.swift`: `stage` and `photo` lose `private`, so the composer stages attachments the same way; `PhotoPicker` loses `private` for the same reason.
- `ios/GoRex/Rex/Upload.swift` and `ios/GoRex/Rex/Host.swift`: `upload` takes an optional `batch` of id, index and count, passed to `upload_begin`. A send with one file passes no batch.
- `ios/GoRex/Views/FeaturesView.swift`: the Files & Photos page describes Composer Mode.
- `ios/GoRex/Views/SettingsView.swift`: Terminal's Composer Mode toggle.
- `ios/project.yml` and `ios/GoRex/InfoPlist.xcstrings`: the camera's purpose text also names photos for a terminal.

The send, in order: stage is done at attach time, so `sendNow` uploads each attachment's file with its batch fields, in order, then calls `insert` with the text, waits briefly (150 ms) so TUI agents such as Devin CLI, Codex, and Claude Code finish their bracketed-paste / paste-burst window, clears any held key-bar modifiers, and calls `send("enter")`. The stream of the batch's last file closes after the paths are typed (Rex API revision 11), so awaiting it orders paths before text.

## Alternatives

- **Upload each attachment as it is picked.** The server types a path the moment its file arrives, which mixes paths into the prompt before the user sends. Lost.
- **Type the paths from the app, not the server.** `upload_begin` types them on the server side and there is no flag to hold it back, so the app would send each file outside a batch and still get the paths typed. Lost.
- **A second text field on the terminal.** The terminal is the program's input, not a buffer the app owns; a line typed there is sent at once. Lost.
- **An ad-hoc bar the key bar opens.** A Compose key, a swipe up and ⇧⌘I opened and closed a bar of the same shape; the bar is the mode's instead, one switch in Settings. Lost.
- **A modal sheet, as the app's other editors.** A sheet dismisses the keyboard and hides the terminal the message is for; the bar over the key bar keeps both, as Rex's does. Lost.

## Security

- The camera's purpose text names its second use. The photo picker's access is the system's, as before.
- The change adds no listener and no credential: the batch fields ride the same `upload_begin` call the uploads already make.

## Tests

- `UploadTests` reads the `batch`, `index` and `count` of a composed send's `upload_begin` calls on the `FakeRex`.
- `ComposerUITests` sees no bar by default, with `composerMode` on sees the pinned bar with the mic beside its field, swipes left on the bar to reveal the inline quick-keys strip (`Esc`, `Tab`), and swipes right to return focus to the compose field.
- A simulator screenshot shows the pinned capsule over the key bar, the mic inside.
- On a simulator with a FakeRex or a dev host, a send with two attachments and a text shows the two paths, then the text, submitted once.
- No invariant of `docs/invariants.md` names the upload path.
