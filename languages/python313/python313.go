// Package python313 lints Python sources with ruff checking the 3.13
// dialect (manifest key "python313"). The key, display name, and target
// version are owned by this package — not by the base image or an
// installed interpreter — so forks add or retire Python lines by adding or
// removing python<NN> packages; every line shares the image's single
// pinned ruff binary.
package python313

import (
	"github.com/zinc-sig/linter/languages/internal/ruff"
	"github.com/zinc-sig/linter/linter"
)

// RuffVersion is the ruff release shared by every python<NN> package,
// pinned once in languages/internal/ruff and re-exported here so
// cmd/toolversions (outside the internal package's import range) can feed
// it to the Dockerfile build.
const RuffVersion = ruff.Version

// New returns the python313 language implementation.
func New() linter.Linter { return ruff.New("python313", "Python 3.13", "py313") }
