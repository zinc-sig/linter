// Package languages turns the declarative language manifest
// (manifest.yaml, embedded at build time) into the registry of
// linter.Linter implementations behind cobe-lint. A language is a YAML
// stanza binding a stable key and display name to one of the tool drivers
// under languages/internal/ — adding a language whose driver already
// exists is a manifest edit, not Go code.
package languages

import (
	"bytes"
	_ "embed"
	"errors"
	"fmt"
	"io"
	"maps"
	"regexp"
	"slices"
	"sync"

	"gopkg.in/yaml.v3"

	"github.com/zinc-sig/linter/languages/internal/checkstyle"
	"github.com/zinc-sig/linter/languages/internal/clangtidy"
	"github.com/zinc-sig/linter/languages/internal/govet"
	"github.com/zinc-sig/linter/languages/internal/ruff"
	"github.com/zinc-sig/linter/linter"
)

//go:embed manifest.yaml
var manifestYAML []byte

// manifestVersion is the manifest-schema version this code implements
// (contract §1). manifest.yaml must declare exactly this value: the
// version is the wire contract core keys on, so bumping it is a code
// change with core-side support, never a YAML-only edit.
const manifestVersion = 1

// manifest mirrors manifest.yaml.
type manifest struct {
	Version   int                 `yaml:"version"`
	Languages map[string]language `yaml:"languages"`
	Tools     map[string]string   `yaml:"tools"`
}

// language is one manifest stanza: the display name, the tool driver id,
// and the driver-owned with: options block, kept opaque here so each
// driver arm in build can decode it against its own schema.
type language struct {
	Name string    `yaml:"name"`
	Tool string    `yaml:"tool"`
	With yaml.Node `yaml:"with"`
}

// ruffOptions is the with: block a tool: ruff stanza owns.
type ruffOptions struct {
	// Target is the --target-version dialect, e.g. py313.
	Target string `yaml:"target"`
}

// clangtidyOptions is the with: block a tool: clang-tidy stanza owns.
type clangtidyOptions struct {
	// Std is the -std= language standard, e.g. gnu++11.
	Std string `yaml:"std"`
}

// decodeWith strictly decodes a stanza's with: block into the driver's
// options struct — an unknown key fails here, so a foreign or misspelled
// option never reaches a driver. An absent block leaves the zero value
// for the arm's required-option checks to report.
func decodeWith(with yaml.Node, out any) error {
	if with.IsZero() {
		return nil
	}
	raw, err := yaml.Marshal(&with)
	if err != nil {
		return fmt.Errorf("with: %w", err)
	}
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	if err := dec.Decode(out); err != nil {
		return fmt.Errorf("with: %w", err)
	}
	return nil
}

// keyRE mirrors core's manifest language-key constraint (contract §1).
var keyRE = regexp.MustCompile(`^[a-z0-9_+-]+$`)

// targetRE and stdRE pin the shape of the option values the drivers
// splice into native argv, so a dialect typo (py3.14, "gnu 17") fails at
// load instead of as an operational failure on every lint run.
var (
	targetRE = regexp.MustCompile(`^py\d+$`)
	stdRE    = regexp.MustCompile(`^[a-z0-9+.]+$`)
)

// pinRE constrains tools: pin values: cmd/toolversions splices them into
// single-quoted shell assignments the Dockerfile sources, so the charset
// must stay quote- and metacharacter-free.
var pinRE = regexp.MustCompile(`^[A-Za-z0-9._+-]+$`)

// build binds one manifest stanza to its tool driver. The driver ids
// match the tool_id each driver stamps into reports; each arm decodes the
// with: block against its own options schema, so a missing, foreign, or
// malformed option fails at load, not at lint time.
func build(key string, l language) (linter.Linter, error) {
	switch l.Tool {
	case ruff.ToolID:
		var opts ruffOptions
		if err := decodeWith(l.With, &opts); err != nil {
			return nil, err
		}
		if opts.Target == "" {
			return nil, fmt.Errorf("tool ruff requires with.target (a --target-version dialect such as py313)")
		}
		if !targetRE.MatchString(opts.Target) {
			return nil, fmt.Errorf("with.target %q does not name a ruff dialect (want e.g. py313)", opts.Target)
		}
		return ruff.New(key, l.Name, opts.Target), nil
	case clangtidy.ToolID:
		var opts clangtidyOptions
		if err := decodeWith(l.With, &opts); err != nil {
			return nil, err
		}
		if opts.Std == "" {
			return nil, fmt.Errorf("tool clang-tidy requires with.std (a -std= language standard such as gnu++11)")
		}
		if !stdRE.MatchString(opts.Std) {
			return nil, fmt.Errorf("with.std %q is not a -std= value (want e.g. gnu++11)", opts.Std)
		}
		return clangtidy.New(key, l.Name, opts.Std), nil
	case checkstyle.ToolID:
		if !l.With.IsZero() {
			return nil, fmt.Errorf("tool %q takes no with: options", l.Tool)
		}
		return checkstyle.New(key, l.Name), nil
	case govet.ToolID:
		if !l.With.IsZero() {
			return nil, fmt.Errorf("tool %q takes no with: options", l.Tool)
		}
		return govet.New(key, l.Name), nil
	default:
		return nil, fmt.Errorf("unknown tool %q (supported: %s, %s, %s, %s)",
			l.Tool, ruff.ToolID, clangtidy.ToolID, checkstyle.ToolID, govet.ToolID)
	}
}

// registry is a validated manifest with its linters built.
type registry struct {
	version int
	tools   map[string]string
	linters []linter.Linter
}

// parse decodes and validates a manifest document and builds one
// linter.Linter per language, in key order.
func parse(data []byte) (*registry, error) {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	var m manifest
	if err := dec.Decode(&m); err != nil {
		return nil, fmt.Errorf("invalid manifest YAML: %w", err)
	}
	// A stray second `---` document would otherwise be dropped silently.
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("manifest must be a single YAML document")
	}
	if m.Version != manifestVersion {
		return nil, fmt.Errorf("manifest version %d is unsupported (this code implements version %d)", m.Version, manifestVersion)
	}
	if len(m.Languages) == 0 {
		return nil, fmt.Errorf("manifest declares no languages")
	}
	for _, tool := range slices.Sorted(maps.Keys(m.Tools)) {
		if !pinRE.MatchString(m.Tools[tool]) {
			return nil, fmt.Errorf("tool pin %s: %q is not a plain version string", tool, m.Tools[tool])
		}
	}
	r := &registry{version: m.Version, tools: m.Tools}
	for _, key := range slices.Sorted(maps.Keys(m.Languages)) {
		spec := m.Languages[key]
		if !keyRE.MatchString(key) {
			return nil, fmt.Errorf("language key %q is invalid (must match %s)", key, keyRE)
		}
		if spec.Name == "" {
			return nil, fmt.Errorf("language %q: name is required", key)
		}
		l, err := build(key, spec)
		if err != nil {
			return nil, fmt.Errorf("language %q: %w", key, err)
		}
		r.linters = append(r.linters, l)
	}
	return r, nil
}

// load parses the embedded manifest once. The file is baked in at build
// time and validated by this package's tests, so a failure here is a
// programming error, not runtime input.
var load = sync.OnceValue(func() *registry {
	r, err := parse(manifestYAML)
	if err != nil {
		panic(fmt.Sprintf("languages: embedded manifest.yaml: %v", err))
	}
	return r
})

// All returns one implementation per supported language, in key order;
// Linter.Language() is the manifest key.
func All() []linter.Linter {
	return slices.Clone(load().linters)
}

// ManifestVersion is the manifest-schema version manifest.yaml declares,
// emitted by `cobe-lint manifest` (contract §1).
func ManifestVersion() int {
	return load().version
}

// ToolVersions returns the toolchain install pins manifest.yaml declares
// (tool id → release), consumed by cmd/toolversions for the Dockerfile
// build.
func ToolVersions() map[string]string {
	return maps.Clone(load().tools)
}
