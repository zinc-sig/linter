// Command sqlsyntax reports the syntax errors in SQL files, for one SQL
// dialect per run. It prints one JSON document on stdout and exits 0 when
// every file is clean, 1 when it reported an error in a file, and 2 when
// it could not run (bad usage, unreadable file).
//
// The check is syntax only: no database is consulted, so an unknown table
// or column is not reported.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"runtime/debug"
	"strings"
)

// problem is one syntax error in a text, as byte offsets into it. end is
// exclusive and equals start when the error has no extent.
type problem struct {
	start, end int
	message    string
}

// checkers maps a dialect name to the function that finds the syntax
// errors of a text in that dialect.
var checkers = map[string]func(sql string) []problem{
	"mysql":      checkMySQL,
	"postgresql": checkPostgreSQL,
}

type diagnostic struct {
	File      string `json:"file"`
	Line      int    `json:"line"`
	Column    int    `json:"column"`
	EndLine   int    `json:"end_line"`
	EndColumn int    `json:"end_column"`
	Message   string `json:"message"`
}

type document struct {
	Diagnostics []diagnostic `json:"diagnostics"`
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("sqlsyntax", flag.ContinueOnError)
	flags.SetOutput(stderr)
	dialect := flags.String("dialect", "", "SQL dialect: mysql or postgresql")
	version := flags.Bool("version", false, "print the parser versions and exit")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if *version {
		fmt.Fprintln(stdout, "sqlsyntax "+parserVersions())
		return 0
	}
	check, ok := checkers[*dialect]
	if !ok {
		fmt.Fprintf(stderr, "sqlsyntax: --dialect must be mysql or postgresql, got %q\n", *dialect)
		return 2
	}
	if flags.NArg() == 0 {
		fmt.Fprintln(stderr, "sqlsyntax: no input files")
		return 2
	}

	doc := document{Diagnostics: []diagnostic{}}
	for _, path := range flags.Args() {
		raw, err := os.ReadFile(path)
		if err != nil {
			fmt.Fprintf(stderr, "sqlsyntax: %v\n", err)
			return 2
		}
		sql := string(raw)
		index := newLineIndex(sql)
		for _, p := range check(sql) {
			d := diagnostic{File: path, Message: p.message}
			d.Line, d.Column = index.position(p.start)
			// A range that runs past its first line marks that line only.
			d.EndLine, d.EndColumn = index.lastPosition(p.start, min(p.end, index.lineEnd(p.start)))
			doc.Diagnostics = append(doc.Diagnostics, d)
		}
	}
	if err := json.NewEncoder(stdout).Encode(doc); err != nil {
		fmt.Fprintf(stderr, "sqlsyntax: %v\n", err)
		return 2
	}
	if len(doc.Diagnostics) > 0 {
		return 1
	}
	return 0
}

// parserVersions names the parser module behind each dialect, read from
// the build information, as "module@version" words.
func parserVersions() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "unknown"
	}
	var parts []string
	for _, dep := range info.Deps {
		switch dep.Path {
		case "github.com/pganalyze/pg_query_go/v6":
			parts = append(parts, "pg_query_go@"+dep.Version)
		case "github.com/pingcap/tidb/pkg/parser":
			parts = append(parts, "tidb-parser@"+dep.Version)
		}
	}
	if len(parts) == 0 {
		return "unknown"
	}
	return strings.Join(parts, " ")
}
