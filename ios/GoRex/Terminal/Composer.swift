import SwiftUI
import UIKit
import UniformTypeIdentifiers

/// The compose sheet's state (docs/IOS-025-composer.md): the message, the
/// attachments staged in a folder of the temporary directory, and a send
/// under way. The folder goes when the sheet closes, sent or not.
@MainActor final class Composer: ObservableObject {
    /// A staged attachment: its file in the folder, and its thumbnail for
    /// the row, nil for a file that is no image.
    struct Attachment: Identifiable {
        let url: URL
        let thumb: UIImage?
        var id: URL { url }
    }

    @Published var text = ""
    @Published var attachments: [Attachment] = []
    /// A send is up: Send spins and the attach buttons hold still.
    @Published var sending = false
    /// Whether the host takes uploads; the attach buttons stay away else.
    let canUpload: Bool
    /// The folder the attachments stage in, removed as the sheet closes.
    private let dir: URL
    /// Whether the terminal's keyboard was up; it comes back as the sheet
    /// closes, if it was.
    private let typing: Bool
    /// The next attachment's staging folder's number.
    private var staged = 0
    /// The pickers' delegates, held while their picker is up.
    private var filePick: FilePick?
    private var shot: CameraShot?
    private weak var terminal: TerminalView?

    init(terminal: TerminalView, typing: Bool) {
        self.terminal = terminal
        self.typing = typing
        canUpload = terminal.session.host.canUpload
        dir = FileManager.default.temporaryDirectory.appending(path: "Composer").appending(path: UUID().uuidString)
    }

    /// The photo picker over the sheet; each pick is staged as its
    /// original file and joins the row, as Add Photo's are.
    func addPhotos() {
        terminal?.present(PhotoPicker { [weak self] providers in
            guard let self, !providers.isEmpty else { return }
            Task {
                do {
                    for p in providers {
                        let url = try await TerminalView.photo(p, into: nextDir())
                        attachments.append(Attachment(url: url, thumb: UIImage(contentsOfFile: url.path())))
                    }
                } catch {
                    failed(error)
                }
            }
        })
    }

    /// The camera over the sheet, where the device has one; the shot is
    /// written as a JPEG and joins the row.
    func takePhoto() {
        guard UIImagePickerController.isSourceTypeAvailable(.camera) else { return }
        let p = UIImagePickerController()
        p.sourceType = .camera
        let shot = CameraShot { [weak self] image in
            guard let self, let image else { return }
            do {
                guard let data = image.jpegData(compressionQuality: 0.9) else { throw CocoaError(.fileWriteUnknown) }
                let into = nextDir()
                try FileManager.default.createDirectory(at: into, withIntermediateDirectories: true)
                let url = into.appending(path: Self.shotName())
                try data.write(to: url)
                attachments.append(Attachment(url: url, thumb: image))
            } catch {
                failed(error)
            }
        }
        self.shot = shot
        p.delegate = shot
        terminal?.present(p)
    }

    /// A shot's file name, as the camera's are.
    private static func shotName() -> String {
        let f = DateFormatter()
        f.dateFormat = "yyyyMMdd-HHmmss"
        return "Photo-\(f.string(from: Date())).JPG"
    }

    /// The document picker over the sheet; its copies move into the
    /// folder and join the row, as Add File's do.
    func addFiles() {
        let p = UIDocumentPickerViewController(forOpeningContentTypes: [.item], asCopy: true)
        p.allowsMultipleSelection = true
        let pick = FilePick { [weak self] urls in
            guard let self else { return }
            let into = nextDir()
            Task {
                do {
                    let moved = try await Task.detached { try TerminalView.stage(urls, into: into) }.value
                    attachments.append(contentsOf: moved.map { Attachment(url: $0, thumb: nil) })
                } catch {
                    failed(error)
                }
            }
        }
        filePick = pick
        p.delegate = pick
        terminal?.present(p)
    }

    /// The staging folder of the next attachment.
    private func nextDir() -> URL {
        defer { staged += 1 }
        return dir.appending(path: String(staged))
    }

    /// Takes an attachment out of the row, and its file with it.
    func remove(_ a: Attachment) {
        attachments.removeAll { $0.id == a.id }
        let url = a.url.deletingLastPathComponent()
        Task.detached { try? FileManager.default.removeItem(at: url) }
    }

    /// Uploads the attachments as one batch, whose paths the server types
    /// together once every file is up, then pastes the text and sends
    /// Return: the paths land first and the lot is one submission. A send
    /// with no text submits the paths alone. Tells whether it went, so
    /// the sheet can close; a failure keeps it open.
    func send() async -> Bool {
        guard let terminal, !sending else { return false }
        sending = true
        defer { sending = false }
        terminal.bar.uploading += 1
        defer { terminal.bar.uploading -= 1 }
        do {
            let files = attachments.map(\.url)
            let batchID = "composer-\(UUID().uuidString.lowercased())"
            for (i, f) in files.enumerated() {
                try await terminal.session.host.upload(f, block: terminal.session.block, session: terminal.session.session,
                                                       batch: files.count > 1 ? (batchID, i, files.count) : nil)
            }
        } catch {
            terminal.alert(String(localized: "Can't Upload"), error.localizedDescription)
            return false
        }
        if !text.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty {
            terminal.insert(text)
        }
        terminal.send("enter")
        return true
    }

    /// The sheet closed, sent or not: the staging folder goes, and the
    /// keyboard comes back to the terminal if it was up.
    func closed() {
        let dir = dir
        Task.detached { try? FileManager.default.removeItem(at: dir) }
        if typing { terminal?.becomeFirstResponder() }
    }

    private func failed(_ error: Error) {
        terminal?.alert(String(localized: "Can't Upload"), error.localizedDescription)
    }
}

/// The compose sheet (docs/IOS-025-composer.md): a message field, a row
/// of the staged attachments, and Photo, Camera and File buttons where
/// the host takes uploads.
struct ComposerView: View {
    @ObservedObject var composer: Composer
    @Environment(\.dismiss) private var dismiss
    @FocusState private var focused: Bool

    /// Send stays off while the message is empty and nothing is staged.
    private var canSend: Bool {
        !composer.sending
            && (!composer.text.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty || !composer.attachments.isEmpty)
    }

    var body: some View {
        NavigationStack {
            VStack(alignment: .leading, spacing: 12) {
                TextField("Message", text: $composer.text, axis: .vertical)
                    .lineLimit(5...14)
                    .focused($focused)
                if !composer.attachments.isEmpty {
                    ScrollView(.horizontal) {
                        HStack(spacing: 12) {
                            ForEach(composer.attachments) { cell($0) }
                        }
                    }
                    .scrollIndicators(.hidden)
                    .padding(.top, 6)
                }
                if composer.canUpload {
                    HStack(spacing: 16) {
                        attach("photo", String(localized: "Photo")) { composer.addPhotos() }
                        if UIImagePickerController.isSourceTypeAvailable(.camera) {
                            attach("camera", String(localized: "Camera")) { composer.takePhoto() }
                        }
                        attach("folder", String(localized: "File")) { composer.addFiles() }
                    }
                    .disabled(composer.sending)
                }
            }
            .padding(16)
            .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .topLeading)
            .navigationTitle(String(localized: "Compose"))
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Cancel") { dismiss() }
                        .disabled(composer.sending)
                }
                ToolbarItem(placement: .confirmationAction) {
                    if composer.sending {
                        ProgressView()
                    } else {
                        Button("Send") { Task { if await composer.send() { dismiss() } } }
                            .disabled(!canSend)
                    }
                }
            }
        }
        .onAppear { focused = true }
        .onDisappear { composer.closed() }
        // A send must not lose its folder mid-upload.
        .interactiveDismissDisabled(composer.sending)
    }

    /// An attachment's thumbnail, or a document mark and its name, with a
    /// mark that takes it out of the row.
    private func cell(_ a: Composer.Attachment) -> some View {
        ZStack(alignment: .topTrailing) {
            Group {
                if let t = a.thumb {
                    Image(uiImage: t)
                        .resizable()
                        .scaledToFill()
                } else {
                    VStack(spacing: 4) {
                        Image(systemName: "doc.fill")
                            .font(.title3)
                            .foregroundStyle(.secondary)
                        Text(a.url.lastPathComponent)
                            .font(.caption2)
                            .foregroundStyle(.secondary)
                            .lineLimit(1)
                    }
                    .padding(4)
                }
            }
            .frame(width: 56, height: 56)
            .background(.quaternary)
            .clipShape(RoundedRectangle(cornerRadius: 10, style: .continuous))
            Button { composer.remove(a) } label: {
                Image(systemName: "xmark.circle.fill")
                    .font(.callout)
                    .symbolRenderingMode(.palette)
                    .foregroundStyle(.white, .gray)
            }
            .accessibilityLabel(String(localized: "Remove Attachment"))
            .offset(x: 6, y: -6)
        }
    }

    /// An attach button: its symbol, read out by its label.
    private func attach(_ icon: String, _ label: String, _ action: @escaping () -> Void) -> some View {
        Button(action: action) {
            Image(systemName: icon)
                .frame(width: 20, height: 20)
        }
        .buttonStyle(.bordered)
        .accessibilityLabel(label)
    }
}

/// The camera's delegate for the composer: its shot, or nil on Cancel.
private final class CameraShot: NSObject, UIImagePickerControllerDelegate, UINavigationControllerDelegate {
    private let picked: (UIImage?) -> Void

    init(picked: @escaping (UIImage?) -> Void) { self.picked = picked }

    func imagePickerController(_ picker: UIImagePickerController, didFinishPickingMediaWithInfo info: [UIImagePickerController.InfoKey: Any]) {
        picked(info[.originalImage] as? UIImage)
        picker.dismiss(animated: true)
    }

    func imagePickerControllerDidCancel(_ picker: UIImagePickerController) {
        picker.dismiss(animated: true)
    }
}

/// The document picker's delegate for the composer: the copies it made.
private final class FilePick: NSObject, UIDocumentPickerDelegate {
    private let picked: ([URL]) -> Void

    init(picked: @escaping ([URL]) -> Void) { self.picked = picked }

    func documentPicker(_ controller: UIDocumentPickerViewController, didPickDocumentsAt urls: [URL]) {
        picked(urls)
    }
}

extension TerminalView {
    /// Opens the compose sheet (docs/IOS-025-composer.md), once: from the
    /// key bar's Compose key, a swipe up on the bar, or ⇧⌘I. The keyboard
    /// yields to the sheet.
    func compose() {
        guard shown?.presentedViewController == nil else { return }
        let typing = isFirstResponder
        resignFirstResponder()
        let sheet = UIHostingController(rootView: ComposerView(composer: Composer(terminal: self, typing: typing)))
        if let s = sheet.sheetPresentationController {
            s.detents = [.medium(), .large()]
            s.prefersGrabberVisible = true
        }
        present(sheet)
    }
}
