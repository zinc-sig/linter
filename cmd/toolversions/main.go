// Command toolversions prints the toolchain version pins declared in
// languages/manifest.yaml as shell variable assignments. The Dockerfile
// build stage runs it to generate /out/tool-versions.sh, which the runtime
// stage's install steps source — so the manifest stays the single home of
// each pin. This binary never ships in the runtime image.
package main

import (
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"

	"github.com/zinc-sig/linter/languages"
)

// vars maps the manifest's tool pins onto the shell variables the
// Dockerfile consumes, in output order.
var vars = []struct{ tool, shellVar string }{
	{"ruff", "RUFF_VERSION"},
	{"checkstyle", "CHECKSTYLE_VERSION"},
	{"go", "GO_VERSION"},
}

func main() {
	pins := languages.ToolVersions()
	for _, v := range vars {
		if pins[v.tool] == "" {
			fmt.Fprintf(os.Stderr, "toolversions: languages/manifest.yaml declares no %s pin\n", v.tool)
			os.Exit(1)
		}
		fmt.Printf("%s='%s'\n", v.shellVar, pins[v.tool])
		delete(pins, v.tool)
	}
	// A pin nothing consumes means the vars list above was not taught
	// about it — fail the build here rather than let a Dockerfile install
	// step expand an empty variable layers later.
	if leftover := slices.Sorted(maps.Keys(pins)); len(leftover) > 0 {
		fmt.Fprintf(os.Stderr, "toolversions: manifest pins with no shell variable in the vars list: %s\n", strings.Join(leftover, ", "))
		os.Exit(1)
	}
}
