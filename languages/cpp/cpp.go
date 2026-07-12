// Package cpp lints C++ sources with clang-tidy.
package cpp

import (
	"github.com/zinc-sig/linter/languages/internal/clangtidy"
	"github.com/zinc-sig/linter/linter"
)

// New returns the cpp language implementation.
func New() linter.Linter { return clangtidy.New("cpp", "solution.cpp") }
