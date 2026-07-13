// Command toolversions prints the toolchain version pins exported by the
// language packages as shell variable assignments. The Dockerfile build
// stage runs it to generate /out/tool-versions.sh, which the runtime
// stage's install steps source — so each languages/<lang> package is the
// single home of its pin. This binary never ships in the runtime image.
package main

import (
	"fmt"

	"github.com/zinc-sig/linter/languages/golang"
	"github.com/zinc-sig/linter/languages/java"
	"github.com/zinc-sig/linter/languages/python313"
)

func main() {
	fmt.Printf("PYLINT_VERSION='%s'\n", python313.PylintVersion)
	fmt.Printf("CHECKSTYLE_VERSION='%s'\n", java.CheckstyleVersion)
	fmt.Printf("GO_VERSION='%s'\n", golang.GoVersion)
}
