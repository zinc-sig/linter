// Package cpp11 lints C++11 sources with clang-tidy. The clang-tidy binary
// itself comes from Debian 13's repositories, so the base-image pin — not a
// version const here — determines its release (currently LLVM 19).
package cpp11

import (
	"github.com/zinc-sig/linter/languages/internal/clangtidy"
	"github.com/zinc-sig/linter/linter"
)

// CppStandard is the C++ standard clang-tidy checks against, passed
// explicitly as -std=. The GNU dialect (gnu++11 rather than strict c++11)
// matches the image's established dialect choice — c pins gnu17, following
// clang's own gnu++NN defaults.
const CppStandard = "gnu++11"

// New returns the cpp11 language implementation.
func New() linter.Linter { return clangtidy.New("cpp11", "C++11", CppStandard) }
