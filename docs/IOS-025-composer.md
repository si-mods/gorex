---
name: IOS-025
description: "Rex Go's composer is a sheet that writes a message, attaches photos, camera shots and files, and sends the lot as one prompt: the attachment paths first, then the text and Return. Use when you change the compose sheet, its attachments, or the batch upload that carries them."
metadata:
  created: "2026-11-25"
  updated: "2026-11-25"
---

# IOS-025: A composer for messages with attachments

Status: proposal.

This doc adds the composer to Rex Go. A user writes a message, attaches photos, camera shots and files, and sends them to the terminal in one submission. It extends the uploads of Add Photo and Add File (`ios/GoRex/Terminal/TerminalView+Upload.swift`).

## Problem

An agent such as Claude Code or Codex takes a message and the images it talks about in one prompt. On the phone a user cannot do that: Add Photo pastes a path the moment the file is up, before the message is written. The user must send the paths alone, or write the text first and hope the paths land after it. There is also no place for a message of more than one line: the terminal types every Return.

## Goal

- A swipe up on the key bar, a Compose key, or ⇧⌘I opens a compose sheet.
- The sheet takes a message of many lines, and attaches photos, camera shots and files.
- Send uploads the attachments, lets the server type their paths, then pastes the text and presses Return: one submission, paths first.
- A host that takes no uploads (`canUpload` off) still composes text, without attachments.

## Non-goals

- A draft kept between opens. A sheet that closes unsent loses its text and its attachments.
- Sending attachments without Return, or paths the user edits before they go.
- Attachments that already sit on the host. Every attachment uploads at Send.

## Design

### Behavior

- The compose sheet opens three ways: a swipe up on the key bar, the Compose key (the free slot of the key bar's last page, the `square.and.pencil` symbol), and ⇧⌘I on a hardware keyboard. The ⌘ menu of the key bar lists Compose first, which is how an iPad reaches it.
- The sheet shows a text field of many lines, a row of the attachments, and the buttons Photo, Camera and File. Cancel is on the left, Send on the right.
- Photo opens the photo picker of Add Photo. Camera opens the camera where one is available. File opens the document picker.
- Each attachment is a thumbnail in the row: the image itself, or a document mark and the file's name. A tap on its mark removes it.
- Send stays off while the text is empty and no attachment is staged. While the files go up, Send spins and everything stays.
- The attachments go up as one batch. When every file has arrived, the server types their paths into the terminal, as it does for Add Photo. Then the app pastes the text and sends Return, through the same input path a paste and the Enter key take.
- A failure keeps the sheet open and says why, so the user can try again. A send that goes through closes the sheet.
- A host that takes no uploads shows the sheet without its attach buttons. Send pastes the text and presses Return.

### Model

- The sheet's state is `Composer`: the text, the staged `Attachment`s, and whether a send is running.
- An `Attachment` is a file staged in a folder of the temporary directory, with a thumbnail for the row. Photos are staged as their original files, as Add Photo's are. A camera shot is written as a JPEG. A file is moved in from the picker's copy.
- The folder goes when the sheet closes, sent or not.

### Code

- `ios/GoRex/Terminal/Composer.swift`: `Composer`, `ComposerView` and `TerminalView.compose`, which presents the sheet. The camera's and the document picker's delegate objects live here too.
- `ios/GoRex/Terminal/TerminalView+Upload.swift`: `stage` and `photo` lose `private`, so `Composer` stages attachments the same way; `PhotoPicker` loses `private` for the same reason.
- `ios/GoRex/Rex/Upload.swift` and `ios/GoRex/Rex/Host.swift`: `upload` takes an optional `batch` of id, index and count, passed to `upload_begin`. A send with one file passes no batch.
- `ios/GoRex/Terminal/TerminalView.swift`: the `KeyBar` gets the Compose key on its last page, Compose in the ⌘ menu, and a swipe-up gesture that calls `compose`.
- `ios/GoRex/Terminal/TerminalView+Commands.swift`: a Compose command on ⇧⌘I.
- `ios/GoRex/Views/FeaturesView.swift`: the key bar's description names the compose sheet.
- `ios/project.yml` and `ios/GoRex/InfoPlist.xcstrings`: the camera's purpose text also names photos for a terminal.

The send, in order: stage is done at attach time, so `send` only uploads each attachment's file with its batch fields, in order, then calls `insert` with the text and `send("enter")`. The stream of the batch's last file closes after the paths are typed (Rex API revision 11), so awaiting it orders paths before text.

## Alternatives

- **Upload each attachment as it is picked.** The server types a path the moment its file arrives, which mixes paths into the prompt before the user sends. Lost.
- **Type the paths from the app, not the server.** `upload_begin` types them on the server side and there is no flag to hold it back, so the app would send each file outside a batch and still get the paths typed. Lost.
- **A second text field on the terminal.** The terminal is the program's input, not a buffer the app owns; a line typed there is sent at once. Lost.

## Security

- The camera's purpose text names its second use. The photo picker's access is the system's, as before.
- The change adds no listener and no credential: the batch fields ride the same `upload_begin` call the uploads already make.

## Tests

- `UploadTests` reads the `batch`, `index` and `count` of a composed send's `upload_begin` calls on the `FakeRex`.
- A simulator screenshot shows the sheet, its attachment row, and the Compose key on the key bar's last page.
- On a simulator with a FakeRex or a dev host, a send with two attachments and a text shows the two paths, then the text, submitted once.
- No invariant of `docs/invariants.md` names the upload path.
