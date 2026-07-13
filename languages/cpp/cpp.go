// Package cpp lints C++ sources with clang-tidy. The clang-tidy binary
// itself comes from Debian 13's repositories, so the base-image pin — not a
// version const here — determines its release (currently LLVM 19).
package cpp

import (
	"github.com/zinc-sig/linter/languages/internal/clangtidy"
	"github.com/zinc-sig/linter/linter"
)

// CppStandard is the C++ standard clang-tidy checks against, passed
// explicitly as -std=. gnu++17 is clang 19's own default for C++ (probed in
// the image via __cplusplus == 201703L with __STRICT_ANSI__ undefined),
// pinned so a future toolchain bump cannot silently move the language level.
const CppStandard = "gnu++17"

// New returns the cpp language implementation.
func New() linter.Linter { return clangtidy.New("cpp", CppStandard) }
