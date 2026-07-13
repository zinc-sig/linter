// Package c lints C sources with clang-tidy. The clang-tidy binary itself
// comes from Debian 13's repositories, so the base-image pin — not a version
// const here — determines its release (currently LLVM 19).
package c

import (
	"github.com/zinc-sig/linter/languages/internal/clangtidy"
	"github.com/zinc-sig/linter/linter"
)

// CStandard is the C standard clang-tidy checks against, passed explicitly
// as -std=. gnu17 is clang 19's own default for C (probed in the image via
// __STDC_VERSION__ == 201710L with __STRICT_ANSI__ undefined), pinned so a
// future toolchain bump cannot silently move the language level.
const CStandard = "gnu17"

// New returns the c language implementation.
func New() linter.Linter { return clangtidy.New("c", CStandard) }
