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
// and the driver's options (target for ruff, std for clang-tidy).
type language struct {
	Name   string `yaml:"name"`
	Tool   string `yaml:"tool"`
	Target string `yaml:"target"`
	Std    string `yaml:"std"`
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
// match the tool_id each driver stamps into reports; every driver rejects
// missing and foreign options, so a manifest typo fails at load, not at
// lint time.
func build(key string, l language) (linter.Linter, error) {
	switch l.Tool {
	case ruff.ToolID:
		if l.Target == "" {
			return nil, fmt.Errorf("tool ruff requires target (a --target-version dialect such as py313)")
		}
		if !targetRE.MatchString(l.Target) {
			return nil, fmt.Errorf("target %q does not name a ruff dialect (want e.g. py313)", l.Target)
		}
		if l.Std != "" {
			return nil, fmt.Errorf("std is a clang-tidy option, not a ruff one")
		}
		return ruff.New(key, l.Name, l.Target), nil
	case clangtidy.ToolID:
		if l.Std == "" {
			return nil, fmt.Errorf("tool clang-tidy requires std (a -std= language standard such as gnu++11)")
		}
		if !stdRE.MatchString(l.Std) {
			return nil, fmt.Errorf("std %q is not a -std= value (want e.g. gnu++11)", l.Std)
		}
		if l.Target != "" {
			return nil, fmt.Errorf("target is a ruff option, not a clang-tidy one")
		}
		return clangtidy.New(key, l.Name, l.Std), nil
	case checkstyle.ToolID:
		if l.Target != "" || l.Std != "" {
			return nil, fmt.Errorf("tool %q takes no options", l.Tool)
		}
		return checkstyle.New(key, l.Name), nil
	case govet.ToolID:
		if l.Target != "" || l.Std != "" {
			return nil, fmt.Errorf("tool %q takes no options", l.Tool)
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
