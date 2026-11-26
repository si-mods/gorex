---
name: IOS-025
description: "Rex Go's Composer Mode is a bar pinned over the key bar that writes a message, attaches photos, camera shots and files, and sends the lot as one prompt: the attachment paths first, then the text and Return. Use when you change the compose bar, its attachments, or the batch upload that carries them."
metadata:
  created: "2026-11-25"
  updated: "2026-11-25"
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

- The bar is up while Composer Mode (`composerMode` in user defaults) is on, pinned over the key bar; there is no ad-hoc way to open or close it — the setting is the only switch. It is read as a view is made, so a change takes effect on a terminal opened after it.
- It is a compact glass capsule with an input row of an attach button, a message field, the mic, and a Send arrow (no decorative grabber on top, as the bar is pinned and does not drag). The terminal grid reserves the bar's resting height in its bottom inset so the bar never covers the program's own input row; as the field grows for multi-line text or staged attachments, the extra height slides the terminal up without resizing the grid. The keyboard stays in view and the field takes its focus.
- The field grows with its lines up to a cap, then scrolls. Its placeholder names the block the message goes to — "Message to Claude Code" — as Rex's does, so a pane is never guessed.
- The mode moves the mic and voice dictation inline: the key bar's right capsule is the stick and the keyboard alone, and the bar's input row holds the screen's own mic between the field and Send. While dictating, the compose bar expands inline into a two-row voice view (leaving the key bar below untouched): the full-width live transcript on top, and a circular Stop button (lands the text in the field for editing), a wide scrolling waveform, and a circular accent Send button (finishes dictation and submits immediately) below.
- The attach button, a plus, opens a menu of Photo, Camera and File, hidden where the host takes no uploads. Photo opens the photo picker of Add Photo, Camera the camera where one is available, File the document picker.
- Each attachment is a thumbnail in a strip over the input row: the image itself, or a document mark. A tap on its mark removes it.
- Send stays off while the text is empty and no attachment is staged. While the files go up, Send spins and the attach menu holds still.
- The attachments go up as one batch. When every file has arrived, the server types their paths into the terminal, as it does for Add Photo. Then the app pastes the text and sends Return, through the same input path a paste and the Enter key take.
- A failure keeps the message and says why, so the user can try again. A send that goes through empties the field, the strip and the staging folder for the next message.

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
- `ComposerUITests` sees no bar by default, and with `composerMode` on sees the pinned bar with the mic beside its field.
- A simulator screenshot shows the pinned capsule over the key bar, the mic inside.
- On a simulator with a FakeRex or a dev host, a send with two attachments and a text shows the two paths, then the text, submitted once.
- No invariant of `docs/invariants.md` names the upload path.
