//go:build ignore

package main

import (
	"fmt"
	"os"
	"path/filepath"

	"gorex/internal/rexproto"
)

func main() {
	files, err := rexproto.GenerateAll()
	if err != nil {
		fmt.Fprintf(os.Stderr, "rexproto: %v\n", err)
		os.Exit(1)
	}
	root := filepath.Join("..", "..")
	for rel, content := range files {
		dst := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.WriteFile(dst, content, 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "write %s: %v\n", dst, err)
			os.Exit(1)
		}
	}
}
