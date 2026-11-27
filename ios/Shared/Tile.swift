import SwiftUI

// The app's tiles, shared with the widgets (GoRexWidgets).

/// A program's tile: a rounded card of its color with a light rim and a
/// sheen along the top, and its glyph when it is in front.
struct ProgramTile: View {
    let program: Program
    var glyph = true
    /// Points to a DIP of the tile: lists use ProgramTiles.scale (1.5),
    /// and the phone's pane bar draws the same tile at its own 21x16 size.
    var scale: CGFloat = 1.5
    /// Whether a light page gets the tile a white ring and a shadow. The
    /// project list sets it off: its tiles stand on the card as they do
    /// on a dark page. Others (pane bar, widgets) keep the ring.
    var plate = true
    @Environment(\.colorScheme) private var scheme

    var body: some View {
        let k = scale
        let card = RoundedRectangle(cornerRadius: 4.5 * k)
        Group {
            if let grad = program.tileBgGradient {
                card.fill(grad.gradient)
            } else {
                card.fill(Color(rgb: program.tileBg))
            }
        }
            .overlay {
                if program.tileBorder { card.strokeBorder(.black.opacity(0.12), lineWidth: 0.5 * k) }
            }
            // Lit from above, as Rex's tiles: a sheen along the top that
            // darkens a little to the foot, and a lit inner edge, brightest
            // at the top.
            .overlay {
                card.fill(LinearGradient(stops: [.init(color: .white.opacity(0.18), location: 0), .init(color: .white.opacity(0), location: 0.5),
                                                 .init(color: .black.opacity(0.06), location: 1)],
                                         startPoint: .top, endPoint: .bottom))
            }
            .overlay {
                card.strokeBorder(LinearGradient(colors: [.white.opacity(0.45), .white.opacity(0.15)], startPoint: .top, endPoint: .bottom),
                                  lineWidth: 0.5)
            }
            .overlay {
                if glyph { ProgramGlyph(program: program).foregroundStyle(Color(rgb: program.tileFg)).frame(width: 11 * k, height: 11 * k) }
            }
            .background {
                // A dark rim on a dark page, a light one on a light page.
                if scheme == .dark || plate {
                    RoundedRectangle(cornerRadius: 4.5 * k + 0.75).fill(scheme == .dark ? .black.opacity(0.7) : .white.opacity(0.95))
                        .padding(scheme == .dark ? -0.75 : -k)
                }
            }
            .shadow(color: .black.opacity(scheme == .dark || plate ? 0.18 : 0), radius: 0.75 * k, y: 0.5 * k)
    }
}

/// The glyph on a program's tile, at any size: an SF Symbol where one is
/// mapped, inset as symbol.go insets them, else the brand's mark (bundled in
/// Brands.xcassets for core brands, or fetched on demand from CDN for extra
/// brands with a terminal fallback while loading).
struct ProgramGlyph: View {
    let program: Program
    @State private var fetched: UIImage?

    var body: some View {
        let g = program.tileGlyph.isEmpty ? program.glyph : program.tileGlyph
        if let sf = Program.symbols[g] {
            Image(systemName: sf).resizable().scaledToFit().scaleEffect(0.85)
        } else {
            let id = g.replacingOccurrences(of: "brand:", with: "")
            if let bundled = UIImage(named: "Brands/" + id, in: Bundle(for: BrandsBundle.self), compatibleWith: nil) {
                Image(uiImage: bundled).renderingMode(.template).resizable().scaledToFit()
            } else if let img = fetched ?? BrandIconCache.shared.cachedImage(for: id) {
                Image(uiImage: img).renderingMode(.template).resizable().scaledToFit()
            } else {
                Image(systemName: "terminal").resizable().scaledToFit().scaleEffect(0.85)
                    .task(id: id) {
                        if let img = await BrandIconCache.shared.fetchIfNeeded(id) {
                            fetched = img
                        }
                    }
            }
        }
    }
}

/// The bundle of Brands.xcassets: the app's or the widgets', and the
/// tests' that draw tiles.
private final class BrandsBundle {}

/// Circular progress indicator for a working program that reports a percentage.
struct ProgressRing: View {
    let progress: Int

    var body: some View {
        let clamped = max(0, min(100, progress))
        Circle().trim(from: 0, to: CGFloat(clamped) / 100)
            .stroke(.secondary, style: StrokeStyle(lineWidth: 2, lineCap: .round))
            .rotationEffect(.degrees(-90))
            .background(Circle().stroke(.quaternary, lineWidth: 2))
            .frame(width: 12, height: 12)
            .accessibilityLabel(Text(verbatim: "\(clamped)%"))
    }
}

