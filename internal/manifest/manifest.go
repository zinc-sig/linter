// Package manifest builds the language manifest document of contract
// section 1, shared by `cobe-lint manifest` and serve's GET /v1/manifest.
package manifest

import "github.com/zinc-sig/linter/linter"

// BinPath is where the Dockerfile installs cobe-lint; the manifest
// advertises it however the CLI was invoked.
const BinPath = "/usr/local/bin/cobe-lint"

// Entry describes one language. The filename a language is staged under
// is core's deployment config and not part of the manifest.
type Entry struct {
	Name    string   `json:"name"`
	Command []string `json:"command"`
}

// Manifest is the section 1 document.
type Manifest struct {
	Version   int              `json:"version"`
	Languages map[string]Entry `json:"languages"`
}

// Build returns the manifest of the given linters under the given
// manifest-schema version.
func Build(version int, all []linter.Linter) Manifest {
	m := Manifest{Version: version, Languages: make(map[string]Entry, len(all))}
	for _, l := range all {
		m.Languages[l.Language()] = Entry{
			Name: l.Name(),
			// A plain argv prefix: callers append one or more
			// workspace-relative file paths as trailing arguments.
			Command: []string{BinPath, "lint", l.Language()},
		}
	}
	return m
}
