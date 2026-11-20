import Foundation
import SwiftUI

/// A color in OKLCH: lightness 0…1, chroma, and hue in degrees, by way of
/// Björn Ottosson's OKLab. Shades of one hue keep it here, where a mix
/// with black in sRGB turns grey.
struct OKLCH: Equatable {
    var l: Double
    var c: Double
    var h: Double

    init(l: Double, c: Double, h: Double) {
        self.l = l
        self.c = c
        self.h = h
    }

    /// An sRGB color as 0xRRGGBB.
    init(rgb: UInt32) {
        func linear(_ v: UInt32) -> Double {
            let c = Double(v & 0xff) / 255
            return c <= 0.04045 ? c / 12.92 : pow((c + 0.055) / 1.055, 2.4)
        }
        let r = linear(rgb >> 16), g = linear(rgb >> 8), b = linear(rgb)
        let l_ = cbrt(0.4122214708 * r + 0.5363325363 * g + 0.0514459929 * b)
        let m_ = cbrt(0.2119034982 * r + 0.6806995451 * g + 0.1073969566 * b)
        let s_ = cbrt(0.0883024619 * r + 0.2817188376 * g + 0.6299787005 * b)
        let L = 0.2104542553 * l_ + 0.7936177850 * m_ - 0.0040720468 * s_
        let A = 1.9779984951 * l_ - 2.4285922050 * m_ + 0.4505937099 * s_
        let B = 0.0259040371 * l_ + 0.7827717662 * m_ - 0.8086757660 * s_
        let h = atan2(B, A) * 180 / .pi
        self.init(l: L, c: (A * A + B * B).squareRoot(), h: h < 0 ? h + 360 : h)
    }

    /// The sRGB components, 0…1; a color outside sRGB is clipped, each
    /// linear component to 0…1.
    var srgb: (r: Double, g: Double, b: Double) {
        let A = c * cos(h * .pi / 180), B = c * sin(h * .pi / 180)
        let l_ = l + 0.3963377774 * A + 0.2158037573 * B
        let m_ = l - 0.1055613458 * A - 0.0638541728 * B
        let s_ = l - 0.0894841775 * A - 1.2914855480 * B
        let lc = l_ * l_ * l_, mc = m_ * m_ * m_, sc = s_ * s_ * s_
        func gamma(_ v: Double) -> Double {
            let c = min(max(v, 0), 1)
            return c <= 0.0031308 ? 12.92 * c : 1.055 * pow(c, 1 / 2.4) - 0.055
        }
        return (gamma(4.0767416621 * lc - 3.3077115913 * mc + 0.2309699292 * sc),
                gamma(-1.2684380046 * lc + 2.6097574011 * mc - 0.3413193965 * sc),
                gamma(-0.0041960863 * lc - 0.7034186147 * mc + 1.7076147010 * sc))
    }

    /// The sRGB color as 0xRRGGBB, each component rounded.
    var rgb: UInt32 {
        let (r, g, b) = srgb
        func byte(_ v: Double) -> UInt32 { UInt32((v * 255).rounded()) }
        return byte(r) << 16 | byte(g) << 8 | byte(b)
    }

    var color: Color {
        let (r, g, b) = srgb
        return Color(red: r, green: g, blue: b)
    }
}
