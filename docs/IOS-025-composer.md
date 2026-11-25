---
name: IOS-025
description: "Rex Go's composer is a bar over the key bar that writes a message, attaches photos, camera shots and files, and sends the lot as one prompt: the attachment paths first, then the text and Return. Use when you change the compose bar, its attachments, or the batch upload that carries them."
metadata:
  created: "2026-11-25"
  updated: "2026-11-25"
---

# IOS-025: A composer for messages with attachments

Status: proposal.

This doc adds the composer to Rex Go, as Rex's iOS app has it. A user writes a message, attaches photos, camera shots and files, and sends them to the terminal in one submission. It extends the uploads of Add Photo and Add File (`ios/GoRex/Terminal/TerminalView+Upload.swift`).

## Problem

An agent such as Claude Code or Codex takes a message and the images it talks about in one prompt. On the phone a user cannot do that: Add Photo pastes a path the moment the file is up, before the message is written. The user must send the paths alone, or write the text first and hope the paths land after it. There is also no place for a message of more than one line: the terminal types every Return.

## Goal

- A swipe up on the key bar, a Compose key, or ⇧⌘I opens a compose bar over it.
- The bar takes a message of many lines, and attaches photos, camera shots and files.
- Send uploads the attachments, lets the server type their paths, then pastes the text and presses Return: one submission, paths first.
- A host that takes no uploads (`canUpload` off) still composes text, without attachments.

## Non-goals

- A draft kept between opens. A bar that closes unsent loses its text and its attachments.
- Sending attachments without Return, or paths the user edits before they go.
- Attachments that already sit on the host. Every attachment uploads at Send.

## Design

### Behavior

- The compose bar opens four ways: a swipe up on the key bar, the Compose key beside ⌘ in the bar's left capsule (the `square.and.pencil` symbol), the ⌘ menu's Compose, and ⇧⌘I on a hardware keyboard, which is how an iPad reaches it.
- It is a glass capsule over the key bar, as Rex's is: a grabber on top, an input row of an attach button, a message field, and a Send arrow. The terminal and the keyboard stay in view; the field takes focus.
- The field grows with its lines up to a cap, then scrolls. Its placeholder names the block the message goes to — "Message Claude Code" — as Rex's does, so a pane is never guessed.
- The attach button, a plus, opens a menu of Photo, Camera and File, hidden where the host takes no uploads. Photo opens the photo picker of Add Photo, Camera the camera where one is available, File the document picker.
- Each attachment is a thumbnail in a strip over the input row: the image itself, or a document mark. A tap on its mark removes it.
- Send stays off while the text is empty and no attachment is staged. While the files go up, Send spins, the attach menu holds still, and the bar does not close.
- The attachments go up as one batch. When every file has arrived, the server types their paths into the terminal, as it does for Add Photo. Then the app pastes the text and sends Return, through the same input path a paste and the Enter key take.
- A failure keeps the bar open and says why, so the user can try again. A send that goes through closes the bar.
- The bar closes on a drag down of its grabber, or a second Compose of a key, the menu or ⇧⌘I; a swipe up on the key bar only opens. Closing removes the staging folder and gives the keyboard back to the terminal, if it had it.

### Model

- The bar is `ComposerBar`, a view of the terminal's host view: the text field, the staged `Attachment`s, and whether a send is running.
- An `Attachment` is a file staged in a folder of the temporary directory, with a thumbnail for the strip. Photos are staged as their original files, as Add Photo's are. A camera shot is written as a JPEG. A file is moved in from the picker's copy.
- The folder goes when the bar closes, sent or not.

### Code

- `ios/GoRex/Terminal/Composer.swift`: `ComposerBar` and `TerminalView.compose`, which toggles it, and `openComposer`, the swipe-up's open. The camera's and the document picker's delegate objects live here too.
- `ios/GoRex/Terminal/TerminalView.swift`: the `KeyBar` gets the Compose key beside ⌘ and Compose in the ⌘ menu, and a swipe-up gesture; `TerminalHost` holds the bar over the key bar.
- `ios/GoRex/Terminal/TerminalView+Upload.swift`: `stage` and `photo` lose `private`, so the composer stages attachments the same way; `PhotoPicker` loses `private` for the same reason.
- `ios/GoRex/Rex/Upload.swift` and `ios/GoRex/Rex/Host.swift`: `upload` takes an optional `batch` of id, index and count, passed to `upload_begin`. A send with one file passes no batch.
- `ios/GoRex/Terminal/TerminalView+Commands.swift`: a Compose command on ⇧⌘I.
- `ios/GoRex/Views/FeaturesView.swift`: the key bar's description names the composer.
- `ios/project.yml` and `ios/GoRex/InfoPlist.xcstrings`: the camera's purpose text also names photos for a terminal.

The send, in order: stage is done at attach time, so `sendNow` only uploads each attachment's file with its batch fields, in order, then calls `insert` with the text and `send("enter")`. The stream of the batch's last file closes after the paths are typed (Rex API revision 11), so awaiting it orders paths before text.

## Alternatives

- **Upload each attachment as it is picked.** The server types a path the moment its file arrives, which mixes paths into the prompt before the user sends. Lost.
- **Type the paths from the app, not the server.** `upload_begin` types them on the server side and there is no flag to hold it back, so the app would send each file outside a batch and still get the paths typed. Lost.
- **A second text field on the terminal.** The terminal is the program's input, not a buffer the app owns; a line typed there is sent at once. Lost.
- **A modal sheet, as the app's other editors.** A sheet dismisses the keyboard and hides the terminal the message is for; the bar over the key bar keeps both, as Rex's does. Lost.

## Security

- The camera's purpose text names its second use. The photo picker's access is the system's, as before.
- The change adds no listener and no credential: the batch fields ride the same `upload_begin` call the uploads already make.

## Tests

- `UploadTests` reads the `batch`, `index` and `count` of a composed send's `upload_begin` calls on the `FakeRex`.
- `ComposerUITests` opens the bar from each of the ⌘ menu, the Compose key and a swipe up on the key bar, and closes it again.
- A simulator screenshot shows the capsule over the key bar and the Compose key beside ⌘.
- On a simulator with a FakeRex or a dev host, a send with two attachments and a text shows the two paths, then the text, submitted once.
- No invariant of `docs/invariants.md` names the upload path.
