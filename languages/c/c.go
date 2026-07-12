// Package c lints C sources with clang-tidy.
package c

import (
	"github.com/zinc-sig/linter/languages/internal/clangtidy"
	"github.com/zinc-sig/linter/linter"
)

// New returns the c language implementation.
func New() linter.Linter { return clangtidy.New("c", "solution.c") }
