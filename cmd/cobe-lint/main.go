// Command cobe-lint is the unified linter CLI baked into the image at
// /usr/local/bin/cobe-lint (docs/CONTRACT.md):
//
//	cobe-lint serve [flags]
//	    the container's main process: the HTTP lint server (sections 7 to 9)
//	cobe-lint dial-stdio [--socket PATH]
//	    bridge stdin and stdout to the server's unix socket (section 10)
//	cobe-lint manifest
//	    print the language-manifest JSON derived from languages/manifest.yaml
//	cobe-lint lint <language> <file> [<file>...]
//	    run the native linter and print unified findings JSON
//
// Exit codes of manifest and lint: 0 = lint ran (findings, even zero, are
// data); 1 = operational failure with a diagnostic on stderr; 2 = usage
// error / unknown language. serve and dial-stdio document their own.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/zinc-sig/linter/internal/dialstdio"
	manifestpkg "github.com/zinc-sig/linter/internal/manifest"
	"github.com/zinc-sig/linter/internal/serve"
	"github.com/zinc-sig/linter/languages"
	"github.com/zinc-sig/linter/linter"
)

// binPath is where the Dockerfile installs this binary; the manifest must
// advertise the baked-in location regardless of how the CLI was invoked.
const binPath = manifestpkg.BinPath

type manifest = manifestpkg.Manifest

func buildManifest(all []linter.Linter) manifest {
	return manifestpkg.Build(languages.ManifestVersion(), all)
}

func usage(w io.Writer) {
	fmt.Fprintln(w, "usage: cobe-lint serve [flags] | cobe-lint dial-stdio [--socket PATH] | cobe-lint manifest | cobe-lint lint <language> <file> [<file>...]")
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		usage(stderr)
		return 2
	}
	switch args[0] {
	case "serve":
		return serve.Main(args[1:], stderr)
	case "dial-stdio":
		return dialstdio.Main(args[1:], stderr)
	case "manifest":
		if len(args) != 1 {
			fmt.Fprintln(stderr, "cobe-lint: manifest takes no arguments")
			return 2
		}
		return emit(stdout, stderr, buildManifest(languages.All()))
	case "lint":
		if len(args) < 3 {
			usage(stderr)
			return 2
		}
		return lint(args[1], args[2:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "cobe-lint: unknown command %q\n", args[0])
		usage(stderr)
		return 2
	}
}

func lint(language string, files []string, stdout, stderr io.Writer) int {
	impl := find(language)
	if impl == nil {
		fmt.Fprintf(stderr, "cobe-lint: unknown language %q (supported: %s)\n",
			language, strings.Join(supported(), ", "))
		return 2
	}
	for _, f := range files {
		if err := checkReadable(f); err != nil {
			fmt.Fprintf(stderr, "cobe-lint: %v\n", err)
			return 1
		}
	}
	report, err := linter.Run(impl, files)
	if err != nil {
		fmt.Fprintf(stderr, "cobe-lint: %v\n", err)
		return 1
	}
	return emit(stdout, stderr, report)
}

func find(language string) linter.Linter {
	for _, l := range languages.All() {
		if l.Language() == language {
			return l
		}
	}
	return nil
}

func supported() []string {
	var keys []string
	for _, l := range languages.All() {
		keys = append(keys, l.Language())
	}
	sort.Strings(keys)
	return keys
}

func checkReadable(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("file not readable: %s", path)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("not a regular file: %s", path)
	}
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("file not readable: %s", path)
	}
	f.Close()
	return nil
}

func emit(stdout, stderr io.Writer, v any) int {
	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		fmt.Fprintf(stderr, "cobe-lint: writing output: %v\n", err)
		return 1
	}
	return 0
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}
