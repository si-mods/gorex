import ImageIO
import SwiftUI
import UIKit

/// A gallery's images full screen, as Photos shows them: swipe for the
/// next, pinch to zoom, pull down to close, tap to show or hide the bars.
/// The top shows the file's name and when it first showed; the bottom
/// shares it.
struct GalleryPager: View {
    let gallery: PaneGallery
    let session: TerminalSession
    @State var current: String
    @State private var chrome = true
    /// The files of the pages loaded, to share.
    @State private var files: [String: URL] = [:]
    @State private var sharing: GalleryShare?
    @Environment(\.dismiss) private var dismiss

    var body: some View {
        ZStack {
            Color.black.ignoresSafeArea()
            TabView(selection: $current) {
                ForEach(gallery.entries) { e in
                    GalleryPage(entry: e, gallery: gallery, session: session,
                                tap: { withAnimation(.easeOut(duration: 0.2)) { chrome.toggle() } },
                                close: { dismiss() },
                                loaded: { files[e.id] = $0 })
                        .tag(e.id)
                }
            }
            .tabViewStyle(.page(indexDisplayMode: .never))
            .ignoresSafeArea()
            .accessibilityIdentifier("gallery.pager")
        }
        .overlay(alignment: .top) { if chrome { header } }
        .overlay(alignment: .bottom) { if chrome { footer } }
        .statusBarHidden(!chrome)
        .environment(\.colorScheme, .dark)
        .sheet(item: $sharing) { s in ShareSheet(items: s.urls).ignoresSafeArea() }
        // The image went from the gallery meanwhile: the next one shows, else
        // the one before, and the pager closes when none is left.
        .onChange(of: gallery.entries) { old, all in
            guard !all.contains(where: { $0.id == current }) else { return }
            let i = old.firstIndex { $0.id == current } ?? old.count
            let near = old[min(i + 1, old.count)...] + old[..<min(i, old.count)].reversed()
            if let next = near.first(where: { n in all.contains { $0.id == n.id } }) {
                current = next.id
            } else {
                dismiss()
            }
        }
    }

    private var entry: GalleryEntry? { gallery.entries.first { $0.id == current } }

    private var header: some View {
        HStack(alignment: .center, spacing: 12) {
            Button("Close", systemImage: "xmark", role: .close) { dismiss() }
                .labelStyle(.iconOnly)
                .buttonStyle(.glass)
                .buttonBorderShape(.circle)
                .controlSize(.large)
            VStack(alignment: .leading, spacing: 2) {
                Text(entry?.name ?? "").font(.headline).lineLimit(1).truncationMode(.middle)
                if let first = entry?.first {
                    Text(first, format: .dateTime.month().day().hour().minute()).font(.caption).foregroundStyle(.secondary)
                }
            }
            Spacer(minLength: 0)
        }
        .padding(.horizontal, 16)
        .padding(.top, 4)
        .transition(.opacity)
    }

    private var footer: some View {
        HStack {
            Button("Share", systemImage: "square.and.arrow.up") {
                if let url = files[current] { sharing = GalleryShare(urls: [url]) }
            }
            .labelStyle(.iconOnly)
            .buttonStyle(.glass)
            .buttonBorderShape(.circle)
            .controlSize(.large)
            .disabled(files[current] == nil)
            Spacer()
        }
        .padding(.horizontal, 16)
        .padding(.bottom, 4)
        .transition(.opacity)
    }
}

/// One image of the pager. An image file of the host downloads as the
/// page shows, with a spinner, and its download goes as the page does; one
/// that fails tells why, in the words of a tap on its path.
private struct GalleryPage: View {
    let entry: GalleryEntry
    let gallery: PaneGallery
    let session: TerminalSession
    let tap: () -> Void
    let close: () -> Void
    /// The page's file to share, and nil as it goes with the page.
    let loaded: (URL?) -> Void
    @State private var image: UIImage?
    @State private var failure: String?
    @State private var download: URL?

    var body: some View {
        ZStack {
            if let image {
                ZoomView(image: image, tap: tap, close: close).ignoresSafeArea()
            } else if let failure {
                ContentUnavailableView {
                    Label("Can't Open File", systemImage: "photo.badge.exclamationmark")
                } description: {
                    Text(failure + "\n" + (entry.path ?? ""))
                }
                .contentShape(Rectangle())
                .onTapGesture(perform: tap)
            } else {
                ProgressView().controlSize(.large).accessibilityLabel("Opening File")
            }
        }
        .task(id: entry.id) { await load() }
        .onDisappear {
            if let download { GalleryImages.discard([download]) }
            download = nil
            loaded(nil)
        }
    }

    /// Three times the screen's longer side in points: as many pixels as
    /// the screen has, and more to zoom into on a 1x or 2x one, without
    /// decoding a large image whole.
    private var side: Int {
        let screen = UIApplication.shared.connectedScenes.compactMap { $0 as? UIWindowScene }.first?.screen.bounds.size
        return Int(max(screen?.width ?? 440, screen?.height ?? 956) * 3)
    }

    private func load() async {
        do {
            let o = try await GalleryImages.original(entry, gallery: gallery, session: session)
            // The page went while its file came: no one deletes it after.
            guard !Task.isCancelled else {
                if o.temporary { GalleryImages.discard([o.url]) }
                return
            }
            if o.temporary { download = o.url }
            let url = o.url, side = side
            image = try await Task.detached {
                try GalleryImages.downsample(CGImageSourceCreateWithURL(url as CFURL, nil), side: side, bounded: false)
            }.value
            guard !Task.isCancelled else { return }
            loaded(url)
        } catch {
            guard !(error is CancellationError) else { return }
            if GalleryImages.isMissing(error, entry) { gallery.drop(missing: entry.id) }
            failure = GalleryImages.reason(error)
        }
    }
}

/// An image a pinch zooms, which closes when pulled down unzoomed and
/// shows or hides the bars at a tap; a double tap zooms in and out.
private struct ZoomView: UIViewRepresentable {
    let image: UIImage
    let tap: () -> Void
    let close: () -> Void

    func makeUIView(context: Context) -> ZoomScroll {
        ZoomScroll(image: image)
    }

    func updateUIView(_ v: ZoomScroll, context: Context) {
        v.tap = tap
        v.close = close
    }
}

private final class ZoomScroll: UIScrollView, UIScrollViewDelegate {
    private let imageView: UIImageView
    var tap: () -> Void = {}
    var close: () -> Void = {}

    init(image: UIImage) {
        imageView = UIImageView(image: image)
        super.init(frame: .zero)
        imageView.contentMode = .scaleAspectFit
        imageView.isAccessibilityElement = true
        imageView.accessibilityTraits = .image
        imageView.accessibilityIdentifier = "gallery.image"
        addSubview(imageView)
        delegate = self
        minimumZoomScale = 1
        maximumZoomScale = 5
        // Pulled down, the image follows the finger, as Photos', and closes.
        alwaysBounceVertical = true
        showsVerticalScrollIndicator = false
        showsHorizontalScrollIndicator = false
        contentInsetAdjustmentBehavior = .never
        let double = UITapGestureRecognizer(target: self, action: #selector(doubleTapped))
        double.numberOfTapsRequired = 2
        let single = UITapGestureRecognizer(target: self, action: #selector(tapped))
        single.require(toFail: double)
        addGestureRecognizer(double)
        addGestureRecognizer(single)
    }

    required init?(coder: NSCoder) { fatalError() }

    override func layoutSubviews() {
        super.layoutSubviews()
        if zoomScale == minimumZoomScale, imageView.frame.size != bounds.size {
            imageView.frame = bounds
            contentSize = bounds.size
        }
    }

    func viewForZooming(in scrollView: UIScrollView) -> UIView? { imageView }

    func scrollViewDidEndDragging(_ scrollView: UIScrollView, willDecelerate decelerate: Bool) {
        if zoomScale <= minimumZoomScale + 0.01, contentOffset.y < -80 { close() }
    }

    @objc private func tapped() { tap() }

    @objc private func doubleTapped(_ g: UITapGestureRecognizer) {
        if zoomScale > minimumZoomScale {
            setZoomScale(minimumZoomScale, animated: true)
        } else {
            let p = g.location(in: imageView)
            let size = CGSize(width: bounds.width / 2.5, height: bounds.height / 2.5)
            zoom(to: CGRect(x: p.x - size.width / 2, y: p.y - size.height / 2, width: size.width, height: size.height), animated: true)
        }
    }
}
