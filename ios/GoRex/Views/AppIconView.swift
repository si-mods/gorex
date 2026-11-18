import SwiftUI

/// The app icons Rex Go ships: Prism, the one it installs with, and the
/// alternates Spectrum and Classic. Each is an Icon Composer bundle next to
/// the app (`AppIcon*.icon`); the image set beside it is a picture of the
/// design, for the app to show.
enum AppIcon: CaseIterable, Identifiable {
    case prism, spectrum, classic

    var id: Self { self }

    /// The name `setAlternateIconName` takes; nil for the default icon. Never
    /// "AppIcon": iOS 26.1 then fails every later change of icon.
    var alternateName: String? {
        switch self {
        case .prism: nil
        case .spectrum: "AppIcon-Spectrum"
        case .classic: "AppIcon-Classic"
        }
    }

    /// The image set that shows the icon in the app.
    var preview: String {
        switch self {
        case .prism: "IconPrism"
        case .spectrum: "IconSpectrum"
        case .classic: "IconClassic"
        }
    }

    var title: LocalizedStringResource {
        switch self {
        case .prism: "Prism"
        case .spectrum: "Spectrum"
        case .classic: "Classic"
        }
    }

    /// The icon on the Home Screen now.
    static var current: AppIcon {
        allCases.first { $0.alternateName == UIApplication.shared.alternateIconName } ?? .prism
    }

    /// The preview, a square, clipped to the shape of an app icon.
    func image(size: CGFloat) -> some View {
        Image(preview).resizable()
            .frame(width: size, height: size)
            .clipShape(.rect(cornerRadius: size * 0.2237, style: .continuous))
    }
}

/// Settings' App Icon page: the icons, a check mark on the one in use. A
/// tap asks iOS to change it; if iOS refuses, the check mark stays and the
/// reason shows under the list a moment.
struct AppIconView: View {
    @Binding var current: AppIcon
    @State private var error: String?
    /// Counts the failures, so that each one shows its message the full time.
    @State private var failures = 0
    /// A change iOS has not answered yet: the rows wait for it.
    @State private var busy = false

    var body: some View {
        List {
            Section {
                ForEach(AppIcon.allCases) { icon in
                    Button { Task { await choose(icon) } } label: {
                        HStack(spacing: 14) {
                            icon.image(size: 60).accessibilityHidden(true)
                            Text(icon.title).foregroundStyle(.primary)
                            Spacer()
                            if icon == current { Image(systemName: "checkmark").fontWeight(.semibold) }
                        }
                    }
                    .listRowBackground(Color.card)
                }
                .disabled(busy || !UIApplication.shared.supportsAlternateIcons)
            } footer: {
                if let error { Text(verbatim: error) }
            }
        }
        .scrollContentBackground(.hidden)
        .background(Color.page)
        .navigationTitle("App Icon")
        .navigationBarTitleDisplayMode(.inline)
        .task(id: failures) {
            try? await Task.sleep(for: .seconds(4))
            error = nil
        }
    }

    private func choose(_ icon: AppIcon) async {
        guard icon != current, !busy else { return }
        busy = true
        defer { busy = false }
        do {
            try await UIApplication.shared.setAlternateIconName(icon.alternateName)
            current = icon
            error = nil
        } catch {
            self.error = error.localizedDescription
            failures += 1
        }
    }
}
