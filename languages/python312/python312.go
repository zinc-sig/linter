// Package python312 lints Python sources with pylint running on a
// uv-managed CPython 3.12 (manifest key "python312"). The key and display
// name are owned by the PythonVersion pin below — not by the base image —
// so forks add or retire interpreter lines by adding or removing
// python<NN> packages.
package python312

import (
	"github.com/zinc-sig/linter/languages/internal/pylint"
	"github.com/zinc-sig/linter/linter"
)

// PythonVersion is the exact CPython release installed for this language
// from uv's standalone builds; pylint for it lives at
// /opt/python/<PythonVersion>/bin/pylint.
const PythonVersion = "3.12.13"

// PylintVersion is the pylint release installed for this interpreter (a
// pip requirement specifier shared by all python packages).
const PylintVersion = pylint.Version

// New returns the python312 language implementation.
func New() linter.Linter { return pylint.New("python312", "Python 3.12", PythonVersion) }
