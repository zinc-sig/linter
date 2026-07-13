// Package cpp14 lints C++14 sources with clang-tidy. The clang-tidy binary
// itself comes from Debian 13's repositories, so the base-image pin — not a
// version const here — determines its release (currently LLVM 19).
package cpp14

import (
	"github.com/zinc-sig/linter/languages/internal/clangtidy"
	"github.com/zinc-sig/linter/linter"
)

// CppStandard is the C++ standard clang-tidy checks against, passed
// explicitly as -std=. The GNU dialect (gnu++14 rather than strict c++14)
// matches the image's established dialect choice — c pins gnu17, following
// clang's own gnu++NN defaults.
const CppStandard = "gnu++14"

// New returns the cpp14 language implementation.
func New() linter.Linter { return clangtidy.New("cpp14", "C++14", CppStandard) }
