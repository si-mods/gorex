//go:build ignore

// Prints the build tags of tsconnect, the wasm build of the pinned
// tailscale.com, for web/build.mjs: go run web/tailnet/tags/tags.go.
package main

import (
	"fmt"

	"tailscale.com/cmd/tsconnect/wasmbuild"
)

func main() { fmt.Print(wasmbuild.Tags()) }
