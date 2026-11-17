import ImageIO
import SwiftUI
import UIKit

/// The first item of the terminal's More menu (docs/ios-pane-gallery.md):
/// the images' count, and whether some are new. It sets a flag the screen
/// reads (galleryCover), as the menu's own views go when it closes.
struct GalleryMenuItem: View {
    let gallery: PaneGallery

    var body: some View {
        Button {
            // The keyboard goes, as it does for a file a tap opens: it
            // would cover the gallery.
            UIApplication.shared.sendAction(#selector(UIResponder.resignFirstResponder), to: nil, from: nil, for: nil)
            gallery.presenting = true
        } label: {
            // A menu shows the second text under the title; in a Label it
            // drops it.
            Text("Gallery")
            Text(gallery.unseen ? "\(gallery.entries.count) Images, New" : "\(gallery.entries.count) Images")
            Image(systemName: "photo.on.rectangle")
        }
    }
}

extension View {
    /// The pane's gallery, full screen in the terminal's scheme.
    func galleryCover(_ session: TerminalSession?, title: String) -> some View {
        modifier(GalleryCover(session: session, title: title))
    }
}

private struct GalleryCover: ViewModifier {
    let session: TerminalSession?
    let title: String
    @Environment(\.colorScheme) private var scheme

    func body(content: Content) -> some View {
        if let session, let gallery = session.gallery {
            content.fullScreenCover(isPresented: Binding { gallery.presenting } set: { gallery.presenting = $0 },
                                    onDismiss: { gallery.opened() }) {
                GalleryView(gallery: gallery, session: session, title: title)
                    .environment(\.colorScheme, scheme)
            }
        } else {
            content
        }
    }
}

/// A pane's gallery (docs/ios-pane-gallery.md): its images in a grid of
/// squares as Photos shows them, the newest at the bottom, where it opens.
struct GalleryView: View {
    let gallery: PaneGallery
    let session: TerminalSession
    let title: String
    @Environment(\.dismiss) private var dismiss
    @State private var viewing: GalleryEntry?
    @State private var selecting = false
    @State private var selected: Set<String> = []
    @State private var clearing = false
    @State private var sharing: GalleryShare?
    /// The downloads of the share sheet up, to delete as it closes.
    @State private var shared: [URL] = []
    @State private var preparing = false
    /// The share's downloads, cancelled as the gallery closes.
    @State private var preparation: Task<Void, Never>?
    @State private var failure: String?

    var body: some View {
        NavigationStack {
            GeometryReader { geo in
                ScrollView {
                    LazyVGrid(columns: columns(geo.size), spacing: 2) {
                        ForEach(gallery.entries) { cell($0) }
                    }
                }
                .defaultScrollAnchor(.bottom, for: .initialOffset)
            }
            .overlay { if gallery.entries.isEmpty { empty } }
            .overlay { if preparing { ProgressView().controlSize(.large) } }
            .navigationTitle(title)
            .navigationSubtitle(Text("\(gallery.entries.count) Images"))
            .navigationBarTitleDisplayMode(.inline)
            .toolbar { toolbar }
        }
        .onAppear { gallery.opened() }
        .onDisappear { preparation?.cancel() }
        .fullScreenCover(item: $viewing) { e in
            GalleryPager(gallery: gallery, session: session, current: e.id)
        }
        .sheet(item: $sharing, onDismiss: { GalleryImages.discard(shared); shared = [] }) { s in
            ShareSheet(items: s.urls).ignoresSafeArea()
        }
        .confirmationDialog("Clear Gallery?", isPresented: $clearing, titleVisibility: .visible) {
            Button("Clear Gallery", role: .destructive) { gallery.clear() }
        } message: {
            Text("The images leave this gallery. Files on the computer stay.")
        }
        .alert("Can't Share", isPresented: Binding { failure != nil } set: { if !$0 { failure = nil } }) {
        } message: {
            Text(failure ?? "")
        }
    }

    /// Three columns upright on a phone and five on its side, as Photos;
    /// on an iPad as many as its width holds.
    private func columns(_ size: CGSize) -> [GridItem] {
        let n = UIDevice.current.userInterfaceIdiom == .pad ? max(4, Int(size.width / 160)) : size.width > size.height ? 5 : 3
        return Array(repeating: GridItem(.flexible(), spacing: 2), count: n)
    }

    private var empty: some View {
        ContentUnavailableView("No Images", systemImage: "photo.on.rectangle",
                               description: Text("Images this pane shows gather here: image files whose paths a program prints, and pictures drawn in the terminal."))
    }

    private func cell(_ e: GalleryEntry) -> some View {
        GalleryCell(entry: e, gallery: gallery, session: session)
            .overlay(alignment: .bottomTrailing) {
                if selecting {
                    Image(systemName: selected.contains(e.id) ? "checkmark.circle.fill" : "circle")
                        .font(.title3)
                        .foregroundStyle(.white, Color.accentColor)
                        .shadow(radius: 2)
                        .padding(4)
                }
            }
            .contentShape(Rectangle())
            .onTapGesture {
                if selecting {
                    if selected.remove(e.id) == nil { selected.insert(e.id) }
                } else {
                    viewing = e
                }
            }
            .contextMenu { if !selecting { menu(e) } }
            .accessibilityElement(children: .ignore)
            .accessibilityLabel(e.name)
            .accessibilityAddTraits(selecting && selected.contains(e.id) ? .isSelected : [])
            .accessibilityIdentifier("gallery.cell")
            .accessibilityAddTraits(.isButton)
    }

    @ViewBuilder private func menu(_ e: GalleryEntry) -> some View {
        if let p = e.path {
            Button("Copy Path", systemImage: "doc.on.doc") { UIPasteboard.general.string = p }
        }
        Button("Share", systemImage: "square.and.arrow.up") { share([e]) }
        Button("Remove from Gallery", systemImage: "trash", role: .destructive) { gallery.remove([e.id]) }
    }

    @ToolbarContentBuilder private var toolbar: some ToolbarContent {
        if selecting {
            ToolbarItem(placement: .confirmationAction) {
                Button("Done") {
                    selecting = false
                    selected = []
                }
            }
            ToolbarItemGroup(placement: .bottomBar) {
                Button("Share", systemImage: "square.and.arrow.up") {
                    share(gallery.entries.filter { selected.contains($0.id) })
                }
                .disabled(selected.isEmpty)
                Spacer()
                Button("Remove from Gallery", systemImage: "trash", role: .destructive) {
                    gallery.remove(selected)
                    selected = []
                }
                .disabled(selected.isEmpty)
            }
        } else {
            ToolbarItem(placement: .cancellationAction) {
                Button("Close", systemImage: "xmark", role: .close) { dismiss() }
            }
            ToolbarItem(placement: .primaryAction) {
                Menu {
                    Button("Select", systemImage: "checkmark.circle") { selecting = true }
                    Button("Clear Gallery", systemImage: "trash", role: .destructive) { clearing = true }
                } label: {
                    Image(systemName: "ellipsis")
                }
                .accessibilityLabel("More")
                .disabled(gallery.entries.isEmpty)
            }
        }
    }

    /// Shares the images' files: a Kitty image's own, an image file of the
    /// host as it downloads now, deleted as the share sheet closes.
    private func share(_ entries: [GalleryEntry]) {
        guard !entries.isEmpty, !preparing else { return }
        preparing = true
        preparation = Task {
            defer { preparing = false }
            var urls: [URL] = []
            var temporary: [URL] = []
            do {
                for e in entries {
                    let o = try await GalleryImages.original(e, gallery: gallery, session: session)
                    urls.append(o.url)
                    if o.temporary { temporary.append(o.url) }
                }
                // The gallery closed while they downloaded.
                try Task.checkCancellation()
                shared = temporary
                sharing = GalleryShare(urls: urls)
            } catch {
                GalleryImages.discard(temporary)
                guard !(error is CancellationError) else { return }
                failure = GalleryImages.reason(error)
            }
        }
    }
}

/// One square of the grid: the image's thumbnail, a placeholder while it
/// loads and one for an image that does not read, and a mark on an image
/// file of the host, as its original stays on the host.
private struct GalleryCell: View {
    let entry: GalleryEntry
    let gallery: PaneGallery
    let session: TerminalSession
    @State private var image: UIImage?
    @State private var failed = false

    var body: some View {
        Color.secondary.opacity(0.15)
            .aspectRatio(1, contentMode: .fit)
            .overlay {
                if let image {
                    // Filled to the square and cut to it, for touches too.
                    Image(uiImage: image).resizable().scaledToFill()
                        .frame(minWidth: 0, maxWidth: .infinity, minHeight: 0, maxHeight: .infinity)
                        .clipped()
                } else if failed {
                    Image(systemName: "photo.badge.exclamationmark").foregroundStyle(.secondary)
                }
            }
            .clipped()
            .overlay(alignment: .bottomLeading) {
                if entry.source == .path {
                    Image(systemName: "doc")
                        .font(.caption2.weight(.semibold))
                        .foregroundStyle(.white)
                        .shadow(color: .black.opacity(0.6), radius: 1.5)
                        .padding(5)
                }
            }
            .task(id: entry.id) {
                do {
                    image = try await GalleryImages.thumbnail(entry, gallery: gallery, session: session)
                } catch {
                    if GalleryImages.isMissing(error, entry) { gallery.drop(missing: entry.id) }
                    if !(error is CancellationError) { failed = true }
                }
            }
    }
}

/// Files to share.
struct GalleryShare: Identifiable {
    let id = UUID()
    let urls: [URL]
}

/// The system's share sheet, which also saves an image to Photos.
struct ShareSheet: UIViewControllerRepresentable {
    let items: [URL]

    func makeUIViewController(context: Context) -> UIActivityViewController {
        UIActivityViewController(activityItems: items, applicationActivities: nil)
    }

    func updateUIViewController(_ vc: UIActivityViewController, context: Context) {}
}

/// The images of a gallery's entries: a Kitty image from its PNG, an
/// image file of the host downloaded as a tap on its path downloads it.
/// A thumbnail of the host's file is kept; its original is not.
enum GalleryImages {
    nonisolated static let thumbSide = 512
    /// The largest file downloaded for a thumbnail: a gallery opens with
    /// the thumbnails of all the cells it shows.
    static let maxThumbBytes = 32 << 20

    static func thumbnail(_ e: GalleryEntry, gallery: PaneGallery, session: TerminalSession) async throws -> UIImage {
        let file = gallery.file(e), thumb = gallery.thumb(e)
        if e.source == .kitty {
            return try await Task.detached { try downsample(CGImageSourceCreateWithURL(file as CFURL, nil), side: thumbSide) }.value
        }
        let kept = await Task.detached { () -> UIImage? in
            guard let d = try? Data(contentsOf: thumb) else { return nil }
            return UIImage(data: d)?.preparingForDisplay()
        }.value
        if let kept { return kept }
        let r = try await gallery.thumbnails.run { try await download(e, session: session, maxBytes: maxThumbBytes) }
        guard let data = r.data else { throw RexError(code: "protocol", message: "the download sent no file") }
        let image = try await Task.detached {
            let image = try downsample(CGImageSourceCreateWithData(data as CFData, nil), side: thumbSide)
            try? FileManager.default.createDirectory(at: thumb.deletingLastPathComponent(), withIntermediateDirectories: true)
            try? image.jpegData(compressionQuality: 0.8)?.write(to: thumb)
            return image
        }.value
        // The image went from the gallery while its thumbnail was made.
        if !gallery.entries.contains(where: { $0.id == e.id }) { try? FileManager.default.removeItem(at: thumb) }
        return image
    }

    /// The image's file to view or share, and whether it is a download to
    /// delete after.
    static func original(_ e: GalleryEntry, gallery: PaneGallery, session: TerminalSession) async throws -> (url: URL, temporary: Bool) {
        if e.source == .kitty {
            // A copy under the name the viewer shows, which a share gives
            // the file, rather than the gallery's own name for it.
            let file = gallery.file(e), name = e.name
            let url = try await Task.detached {
                let dir = OpenedFiles.folder.appending(path: UUID().uuidString)
                try FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
                let url = dir.appending(path: name)
                try FileManager.default.copyItem(at: file, to: url)
                return url
            }.value
            return (url, true)
        }
        let r = try await download(e, session: session, maxBytes: OpenedFiles.maxBytes)
        let path = e.path ?? ""
        return (try await OpenedFiles.save(r, path: path), true)
    }

    private static func download(_ e: GalleryEntry, session: TerminalSession, maxBytes: Int) async throws -> DownloadResult {
        guard let path = e.path, session.host.canDownload else {
            throw RexError(code: "not_supported", message: String(localized: "The host does not send files"))
        }
        return try await session.host.download(path, block: session.block, session: session.session, maxBytes: maxBytes)
    }

    /// Deletes downloads: each is in a folder of its own (OpenedFiles).
    static func discard(_ urls: [URL]) {
        for u in urls { try? FileManager.default.removeItem(at: u.deletingLastPathComponent()) }
    }

    /// Whether the host has no such file at an absolute path: the image
    /// leaves the gallery. A relative path resolves against the program's
    /// directory now, which may have changed since it printed, so its
    /// no_file proves nothing and the entry stays.
    static func isMissing(_ error: Error, _ entry: GalleryEntry) -> Bool {
        (error as? RexError)?.code == "no_file" && entry.absolute
    }

    /// Why an image did not load, in the words of a tap on its path.
    static func reason(_ error: Error) -> String {
        (error as? RexError).map { FileLink.reason(code: $0.code, message: $0.message) } ?? error.localizedDescription
    }

    nonisolated struct Unreadable: LocalizedError {
        var errorDescription: String? { String(localized: "Can't Read Image") }
    }

    /// The image at most side pixels on its longer side, upright, decoded
    /// at that size rather than whole. A thumbnail is not made of one
    /// larger than the scanner keeps, by the size its header declares.
    nonisolated static func downsample(_ src: CGImageSource?, side: Int, bounded: Bool = true) throws -> UIImage {
        guard let src else { throw Unreadable() }
        if bounded {
            guard let props = CGImageSourceCopyPropertiesAtIndex(src, 0, nil) as? [CFString: Any],
                  let w = props[kCGImagePropertyPixelWidth] as? Int, let h = props[kCGImagePropertyPixelHeight] as? Int,
                  GalleryScanner.fits(width: w, height: h) else { throw Unreadable() }
        }
        let options: [CFString: Any] = [
            kCGImageSourceCreateThumbnailFromImageAlways: true,
            kCGImageSourceThumbnailMaxPixelSize: side,
            kCGImageSourceCreateThumbnailWithTransform: true,
            kCGImageSourceShouldCacheImmediately: true,
        ]
        guard let cg = CGImageSourceCreateThumbnailAtIndex(src, 0, options as CFDictionary) else { throw Unreadable() }
        return UIImage(cgImage: cg)
    }
}
