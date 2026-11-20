# Rex Go and GoRex: app icons

Status: built (#808, #814, #871).

## Problem

The icon of Rex Go was one flat bitmap. iOS 26 draws an icon as layers of glass, with highlights, shadows and a light, dark or tinted look of its own. A bitmap gets none of that. This design gives the app an icon made for iOS 26. It also lets the user choose another icon in Settings.

## Design

### The icons

- **Prism** is the default icon. It is drawn in Icon Composer. It shows a glass prism on Ink. A white beam goes in, and a warm fan of light (yellow, orange, pink, cyan) comes out. The desktop app has the same icon (see Desktop).
- **Spectrum** is an alternate icon. It shows the same prism. The light that comes out has seven colours.
- **Classic** is an alternate icon. It is the icon from before this change, with one bitmap layer. People who liked it can keep it.

### Where the files are

All files are in `ios/GoRex`:

- `AppIcon.icon`, `AppIcon-Spectrum.icon` and `AppIcon-Classic.icon` are Icon Composer bundles. Each has an `icon.json` and an `Assets` folder. Open one in Icon Composer to see and edit it.
- `Assets.xcassets/IconPrism.imageset`, `IconSpectrum.imageset` and `IconClassic.imageset` hold a 360 px square picture of each icon. The app shows these pictures (see Previews).
- `Assets.xcassets/LaunchIcon.imageset` holds the Prism preview, clipped to the squircle of the icon at 144 pt. The launch screen centers this image (`UILaunchScreen` in `project.yml`).
  - The launch screen uses `LaunchBackground` as its background. It is the two shades of `Color.page`, so the first frame is the frame of the host list.
- `Assets.xcassets/PrismBody.imageset` and `PrismSpectrum.imageset` hold copies of the two layers of `AppIcon.icon`. `PrismLoading` (in `TerminalScreen`) uses them.
  - While the stream of a terminal opens, `PrismLoading` draws the prism and its light from these images, without the tile of the icon.
  - A mask sweeps the spectrum in and back out. This follows the way of the beam through the glass.
- `Views/AppIconView.swift` holds the `AppIcon` enum and the App Icon page of Settings.
- `Views/SettingsView.swift` holds the App Icon row, and About. About shows the icon in use.

The old `AppIcon.appiconset` is gone. The `.icon` bundle replaces it.

### The layers of the default icon

`AppIcon.icon` is an Icon Composer export. Keep it as it was made. Edit it in Icon Composer, not by hand. It has two groups:

1. `prism2.png` is the prism. The glass is at 50% opacity, with refraction (strength 0.5) and full translucency. The background and the light show through.
2. `color3.png` is the beam and the fan of light. It is behind the prism.

The background is the Ink gradient, in the `fill` of `icon.json`.

### The light follows refraction

In Spectrum, the light is a ray. The design traces it with Snell's law through the outer faces of the prism. These faces are the triangle in `2-prism`, grown by its 60 px corner radius.

- The beam meets the left face and bends toward the normal. Inside the prism, it runs parallel to the base. This is the path of least deviation.
- At the right face, the beam bends away from the normal. It comes out across a short part of that face, not from one point.
- In Spectrum, red bends least and is at the top. Violet bends most and is at the bottom. The colours part at the first face. Inside the glass, the light is already seven bands that spread toward the second face. It is not white.
- The refractive index runs from 1.36 (red) to 1.64 (violet). Real glass spreads the colours by about 1°. That line is too thin to see on an icon. The wider spread keeps every direction and the order true.

If the prism moves or changes shape, trace the light again. A beam that does not bend at both faces reads as wrong.

The layers are flat shapes. The system draws the glass, the highlights, the shadows and the dark and tinted looks. Do not draw them into the SVGs. The system would draw them twice.

### Desktop

The desktop app has the same prism as its icon. `resources/icon.png` (GoRex) and `resources/icon-dev.png` (GoRex Dev) are 1024 px PNGs. They show the layers of the icon, drawn into the squircle of macOS icons (824 px, inset 100 px) with a soft shadow. `tools/mkicon` drew the panes this way before. MyGo makes the macOS, Windows and Linux icons from these PNGs.

- GoRex has the Ink background, as Rex Go has.
- GoRex Dev has a violet-to-magenta gradient behind the same prism. In this way the two icons read apart in the Dock.

These PNGs are renders of the Icon Composer layers. No tool in the repo writes them. When `AppIcon.icon` changes, render them again by hand, as for the previews. `tools/mkicon` now draws only the icons of the web client (`-web`). Those icons still show the panes.

### The setting

Settings has an App Icon row after Themes. The row shows the icon in use and its name. It opens a page with one row for each icon. Each row shows the picture of the icon, its name and a check mark on the icon in use.

- The icon in use is `UIApplication.shared.alternateIconName`. A nil value means Prism.
- A tap calls `UIApplication.shared.setAlternateIconName`. iOS shows its own alert, "You have changed the icon". The app cannot turn this alert off.
  - When the call fails, the check mark stays where it was. The error shows in the footer for four seconds.
- Where `supportsAlternateIcons` is false, the rows are disabled.
- `SettingsView` keeps the icon in use as state and gives the page a binding. In this way the row names the new icon when the page closes.

#### Nil for the default

Set the default icon with nil. **Never** set it with `"AppIcon"`. Developers report that in iOS 26.1 a call with the name of the primary icon works once. Then every later call to change the icon fails until the device restarts. For this reason, `AppIcon.alternateName` is nil for Prism. Do not "simplify" it to the name of the bundle.

### Build

- Use XcodeGen 2.45.1 or later. Earlier versions do not know `.icon` folders. `minimumXcodeGenVersion` in `ios/project.yml` states this.
- `ASSETCATALOG_COMPILER_ALTERNATE_APPICON_NAMES` lists the alternates, `AppIcon-Spectrum AppIcon-Classic`. The primary icon, `AppIcon`, is the default of `ASSETCATALOG_COMPILER_APPICON_NAME`. The list does not include it.
- `ASSETCATALOG_COMPILER_INCLUDE_ALL_APPICON_ASSETS` is `YES`. Without it, the compiler leaves the alternates out of the app.

The name of an alternate is the same string in three places: the build setting, `alternateName` and the folder name (`AppIcon-Spectrum.icon`).

### Previews

The `Icon*.imageset` pictures are drawings of the designs. They are **not** the render of the system. The system makes the glass and the shadows only on the Home Screen. A preview is an unmasked square. The app clips it to the shape of an icon (`AppIcon.image(size:)`: a continuous corner of 22.37 % of the size).

When an `.icon` changes, redraw its preview by hand. If you do not, the page shows an icon that the Home Screen does not.

### Add another icon

1. Make `AppIcon-<Name>.icon` in `ios/GoRex` with Icon Composer.
2. Add `AppIcon-<Name>` to `ASSETCATALOG_COMPILER_ALTERNATE_APPICON_NAMES` in `ios/project.yml`. Run `xcodegen`.
3. Draw a 360 px square preview as `Assets.xcassets/Icon<Name>.imageset`.
4. Add a case to `AppIcon` with its `alternateName`, `preview` and `title`. The page lists `allCases` in the order that the cases are declared.
5. Add the title to `Localizable.xcstrings`, with its `zh-Hans` translation.
6. Add a bullet to each locale of `ios/WhatToTest.md`.
