import SwiftUI

/// A small empty state for card, row and menu sizes, where a whole page's
/// ContentUnavailableView is too much: an optional symbol, a title that
/// names the state, and an optional button at the end for the way out.
struct EmptyState: View {
    let title: LocalizedStringKey
    var symbol: String?
    var button: (title: LocalizedStringKey, action: () -> Void)?

    init(_ title: LocalizedStringKey, symbol: String? = nil, button: (LocalizedStringKey, () -> Void)? = nil) {
        self.title = title
        self.symbol = symbol
        self.button = button
    }

    var body: some View {
        HStack(spacing: 10) {
            if let symbol {
                Image(systemName: symbol)
                    .symbolRenderingMode(.hierarchical)
                    .foregroundStyle(.secondary)
            }
            Text(title)
                .font(.subheadline)
                .foregroundStyle(.secondary)
            Spacer(minLength: 8)
            if let button {
                Button(button.title, action: button.action)
                    .buttonStyle(.glass)
                    .font(.subheadline.weight(.semibold))
            }
        }
    }
}
