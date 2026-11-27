package main

import (
	"path/filepath"
	"testing"
)

func TestGeneratedBrandsUpToDate(t *testing.T) {
	root := filepath.Join("..", "..")
	if err := run(root, true); err != nil {
		t.Fatal(err)
	}
}
