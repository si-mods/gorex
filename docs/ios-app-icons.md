# Rex Go: app icons

Status: implemented.

## Problem

Rex Go's icon was one flat bitmap. iOS 26 draws an icon as layers of glass, with
highlights, shadows and a light, dark or tinted look of its own. A bitmap gets
none of that. This design gives the app an icon made for iOS 26, and lets the
user choose another one in Settings.

## The icons

- **Prism** is the default. A glass prism: white light goes in, and white light
  that fades to silver comes out.
- **Spectrum** is an alternate. The same prism, and the light that comes out is
  in seven colours.
- **Classic** is an alternate. The icon from before this change, one bitmap
  layer, so people who liked it can keep it.

## Where the files are

All are in `ios/GoRex`:

- `AppIcon.icon`, `AppIcon-Spectrum.icon` and `AppIcon-Classic.icon`: Icon
  Composer bundles. Each has an `icon.json` and an `Assets` folder. Open one in
  Icon Composer to see and edit it.
- `Assets.xcassets/IconPrism.imageset`, `IconSpectrum.imageset` and
  `IconClassic.imageset`: a 360 px square picture of each icon, for the app to
  show (see Previews).
- `Views/AppIconView.swift`: the `AppIcon` enum and Settings' App Icon page.
- `Views/SettingsView.swift`: the App Icon row, and About, which shows the
  icon in use.

The old `AppIcon.appiconset` is gone: the `.icon` bundle replaces it.

## The default icon's layers

`AppIcon.icon` has three layers, from the back to the front:

1. `1-beam`: the white light that goes into the prism, and its path inside
   the glass, at 55% opacity.
2. `2-glow`: the light that comes out. A flat shape: the layer's `fill` in
   `icon.json`, a white-to-silver gradient, overrides the SVG's own colour. The
   SVG's `#DCE3F0` only shows in a tool that ignores `icon.json`; change the
   gradient in `icon.json`, not the SVG.
3. `3-prism`: the prism, marked as glass.

## The light follows refraction

The light is a ray traced with Snell's law through the prism's outer faces
(the triangle in `3-prism`, grown by its 60 px corner radius):

- The beam meets the left face and bends toward the normal. Inside, it runs
  parallel to the base: the path of least deviation.
- At the right face it bends away from the normal, and it comes out across a
  short part of that face, not from one point.
- In Spectrum, red bends least and is at the top, violet bends most and is at
  the bottom. The colours part at the first face: inside the glass the light
  is already seven bands that spread toward the second face, not white.
- The refractive index runs from 1.36 (red) to 1.64 (violet). Real glass
  spreads the colours by about 1°, a line too thin to see on an icon; the
  wider spread keeps every direction and the order true.

If the prism moves or changes shape, trace the light again: a beam that does
not bend at both faces reads as wrong.

The layers are flat shapes. The system draws the glass, the highlights, the
shadows and the dark and tinted looks. Do not draw them into the SVGs: they
would be drawn twice.

## The setting

Settings has an App Icon row after Themes. It shows the icon in use and its
name, and opens a page with one row for each icon: its picture, its name and a
check mark on the one in use.

- The icon in use is `UIApplication.shared.alternateIconName`; nil means Prism.
- A tap calls `UIApplication.shared.setAlternateIconName`. iOS shows its own
  alert, "You have changed the icon", and this cannot be turned off. When the
  call fails, the check mark stays where it was and the error shows in the
  footer for four seconds.
- Where `supportsAlternateIcons` is false, the rows are disabled.
- `SettingsView` keeps the icon in use as state and gives the page a binding,
  so the row names the new icon when the page closes.

### Nil for the default

The default icon is set with nil, **never** with `"AppIcon"`. Developers report
that in iOS 26.1 a call with the name of the primary icon works once, and then
every later call to change the icon fails until the device restarts. `AppIcon.alternateName` is nil for Prism for this reason.
Do not "simplify" it to the name of the bundle.

## Build

- XcodeGen 2.45.1 or later: earlier versions do not know `.icon` folders.
  `minimumXcodeGenVersion` in `ios/project.yml` says so.
- `ASSETCATALOG_COMPILER_ALTERNATE_APPICON_NAMES` lists the alternates,
  `AppIcon-Spectrum AppIcon-Classic`. The primary icon, `AppIcon`, is the default
  of `ASSETCATALOG_COMPILER_APPICON_NAME` and is not listed.
- `ASSETCATALOG_COMPILER_INCLUDE_ALL_APPICON_ASSETS` is `YES`: without it, the
  compiler leaves the alternates out of the app.

The names an alternate has in the build setting, in `alternateName` and as its
folder name (`AppIcon-Spectrum.icon`) are the same string.

## Previews

The `Icon*.imageset` pictures are drawings of the designs, **not** the system's
render: the system makes the glass and the shadows only on the Home Screen. A
preview is an unmasked square, and the app clips it to the shape of an icon
(`AppIcon.image(size:)`: a continuous corner of 22.37 % of the size).

When an `.icon` changes, redraw its preview by hand, or the page shows an icon
the Home Screen does not.

## Add another icon

1. Make `AppIcon-<Name>.icon` in `ios/GoRex` with Icon Composer.
2. Add `AppIcon-<Name>` to `ASSETCATALOG_COMPILER_ALTERNATE_APPICON_NAMES` in
   `ios/project.yml`, and run `xcodegen`.
3. Draw a 360 px square preview as `Assets.xcassets/Icon<Name>.imageset`.
4. Add a case to `AppIcon` with its `alternateName`, `preview` and `title`. The
   page lists `allCases` in the order the cases are declared.
5. Add the title to `Localizable.xcstrings`, with its `zh-Hans` translation.
6. Add a bullet to each locale of `ios/WhatToTest.md`.
