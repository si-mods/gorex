# Third-Party Notices

## Ghostty (GhosttyKit, libghostty-vt)

The terminal is a Ghostty surface: the app links GhosttyKit, rootshell's
build of Ghostty, from the binary Swift package
https://github.com/kitknox/ghosttykit-rootshell (0.2.22, from
https://github.com/kitknox/ghostty-rootshell). It also links
libghostty-vt, built from Ghostty's sources by
`scripts/build-ghostty-vt.sh`, which reads the snapshot a terminal stream
starts with. https://github.com/ghostty-org/ghostty

GhosttyKit's license adds to Ghostty's notice below:

```
Copyright (c) 2026 Kit Knox
```

```
MIT License

Copyright (c) 2024 Mitchell Hashimoto, Ghostty contributors

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
```

## rootshell

`scripts/build-ghostty-vt.sh`, the keyboard bar and the text input traits of
`GoRex/Terminal/TerminalView.swift` follow rootshell's
`scripts/build-framework.sh`, `KeyboardAccessoryView` and `TerminalView`;
the Ghostty surface and app in `GoRex/Terminal/TerminalView.swift` and
`GoRex/Terminal/Ghostty.swift` follow its `TerminalSurfaceController`,
`GhosttyApp` and `GhosttyInput`.
https://github.com/kitknox/rootshell

```
MIT License

Copyright (c) 2026 Rootshell LLC, Kit Knox

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
```

## iTerm2-Color-Schemes (terminal themes)

`GoRex/ghostty-themes.json` holds the themes Ghostty ships, from
https://github.com/mbadolato/iTerm2-Color-Schemes, as GoRex's mygo has them
(`scripts/ghostty-themes`). The copyright and license of each theme belong
to its author.

```
MIT License

Copyright (c) 2011 to Present Mark Badolato

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
```

## Simple Icons (brand marks)

The brand marks of `GoRex/Assets.xcassets/Brands`, on the tiles of window
rows, are GoRex's `assets/brands`: Simple Icons, under CC0 1.0 Universal
(public domain). The trademarks remain their owners'. omp's mark is drawn
after omp.sh's favicon. https://simpleicons.org
