package languages

import (
	"slices"
	"strings"
	"testing"
)

func TestRegistry(t *testing.T) {
	all := All()
	var keys []string
	for _, l := range all {
		keys = append(keys, l.Language())
		if l.Name() == "" {
			t.Errorf("%s: empty display Name", l.Language())
		}
		argv := l.Command([]string{"input-file"})
		if len(argv) == 0 {
			t.Errorf("%s: empty Command", l.Language())
		}
		if !slices.Contains(argv, "input-file") {
			t.Errorf("%s: Command %v does not include the input file", l.Language(), argv)
		}
	}
	want := []string{"c", "cpp11", "cpp14", "go", "java", "python312", "python313"}
	if !slices.Equal(keys, want) {
		t.Errorf("registry keys = %v, want %v (sorted)", keys, want)
	}
}

// TestNativeCommands pins each language's exact native argv, so a typo in
// a manifest.yaml stanza (wrong target, wrong std) fails here, not in the
// image.
func TestNativeCommands(t *testing.T) {
	want := map[string][]string{
		"c":         {"clang-tidy", "input-file", "--", "-std=gnu17"},
		"cpp11":     {"clang-tidy", "input-file", "--", "-std=gnu++11"},
		"cpp14":     {"clang-tidy", "input-file", "--", "-std=gnu++14"},
		"go":        {"go", "vet", "input-file"},
		"java":      {"/opt/java/bin/java", "-jar", "/opt/checkstyle.jar", "-c", "/opt/checkstyle-config.xml", "-f", "xml", "input-file"},
		"python312": {"/usr/local/bin/ruff", "check", "--no-cache", "--output-format=json", "--target-version", "py312", "input-file"},
		"python313": {"/usr/local/bin/ruff", "check", "--no-cache", "--output-format=json", "--target-version", "py313", "input-file"},
	}
	for _, l := range All() {
		argv, ok := want[l.Language()]
		if !ok {
			t.Errorf("%s: no expected argv — teach this test about the new language", l.Language())
			continue
		}
		if got := l.Command([]string{"input-file"}); !slices.Equal(got, argv) {
			t.Errorf("%s: Command = %v, want %v", l.Language(), got, argv)
		}
	}
}

func TestNames(t *testing.T) {
	want := map[string]string{
		"c": "C", "cpp11": "C++11", "cpp14": "C++14", "go": "Go",
		"java": "Java", "python312": "Python 3.12", "python313": "Python 3.13",
	}
	for _, l := range All() {
		if name := want[l.Language()]; l.Name() != name {
			t.Errorf("%s: Name = %q, want %q", l.Language(), l.Name(), name)
		}
	}
}

func TestManifestVersion(t *testing.T) {
	if v := ManifestVersion(); v != 1 {
		t.Errorf("ManifestVersion = %d, want 1", v)
	}
}

// The Dockerfile installs these three toolchains from the manifest's pins
// (clang-tidy comes from Debian and has none).
func TestToolVersions(t *testing.T) {
	pins := ToolVersions()
	for _, tool := range []string{"ruff", "checkstyle", "go"} {
		if pins[tool] == "" {
			t.Errorf("ToolVersions missing %q: %v", tool, pins)
		}
	}
}

// TestParseRejects pins the loader's validation: a broken manifest must
// fail at load with a diagnostic naming the problem.
func TestParseRejects(t *testing.T) {
	cases := map[string]struct {
		yaml    string
		wantErr string
	}{
		"unknown field": {
			"version: 1\nlanguages:\n  x:\n    name: X\n    tool: ruff\n    target: py313\n    filename: solution.py\n",
			"field filename not found",
		},
		"unknown tool": {
			"version: 1\nlanguages:\n  x:\n    name: X\n    tool: pylint\n",
			"unknown tool",
		},
		"ruff without target": {
			"version: 1\nlanguages:\n  x:\n    name: X\n    tool: ruff\n",
			"requires target",
		},
		"clang-tidy without std": {
			"version: 1\nlanguages:\n  x:\n    name: X\n    tool: clang-tidy\n",
			"requires std",
		},
		"std on ruff": {
			"version: 1\nlanguages:\n  x:\n    name: X\n    tool: ruff\n    target: py313\n    std: gnu17\n",
			"clang-tidy option",
		},
		"target on clang-tidy": {
			"version: 1\nlanguages:\n  x:\n    name: X\n    tool: clang-tidy\n    std: gnu17\n    target: py313\n",
			"ruff option",
		},
		"option on checkstyle": {
			"version: 1\nlanguages:\n  x:\n    name: X\n    tool: checkstyle\n    std: gnu17\n",
			"takes no options",
		},
		"option on go vet": {
			"version: 1\nlanguages:\n  x:\n    name: X\n    tool: go vet\n    target: py313\n",
			"takes no options",
		},
		"invalid key": {
			"version: 1\nlanguages:\n  Python:\n    name: X\n    tool: ruff\n    target: py313\n",
			"key \"Python\" is invalid",
		},
		"missing name": {
			"version: 1\nlanguages:\n  x:\n    tool: ruff\n    target: py313\n",
			"name is required",
		},
		"missing tool": {
			"version: 1\nlanguages:\n  x:\n    name: X\n",
			"unknown tool",
		},
		"no languages": {
			"version: 1\n",
			"no languages",
		},
		"missing version": {
			"languages:\n  x:\n    name: X\n    tool: ruff\n    target: py313\n",
			"version 0 is unsupported",
		},
		"future version": {
			"version: 2\nlanguages:\n  x:\n    name: X\n    tool: ruff\n    target: py313\n",
			"version 2 is unsupported",
		},
		"malformed target": {
			"version: 1\nlanguages:\n  x:\n    name: X\n    tool: ruff\n    target: py3.13\n",
			"does not name a ruff dialect",
		},
		"malformed std": {
			"version: 1\nlanguages:\n  x:\n    name: X\n    tool: clang-tidy\n    std: \"gnu 17\"\n",
			"is not a -std= value",
		},
		"shell metacharacters in pin": {
			"version: 1\nlanguages:\n  x:\n    name: X\n    tool: ruff\n    target: py313\ntools:\n  ruff: \"0.15.21'; rm -rf /; '\"\n",
			"not a plain version string",
		},
		"second document": {
			"version: 1\nlanguages:\n  x:\n    name: X\n    tool: ruff\n    target: py313\n---\nversion: 1\n",
			"single YAML document",
		},
		"not yaml": {
			"{{nope",
			"invalid manifest YAML",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := parse([]byte(tc.yaml))
			if err == nil {
				t.Fatal("parse succeeded, want error")
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("err = %v, want it to contain %q", err, tc.wantErr)
			}
		})
	}
}

// A valid document exercises every driver binding and the sorted build
// order without touching the embedded manifest.
func TestParseBuildsAllDrivers(t *testing.T) {
	doc := `
version: 1
languages:
  zpy:
    name: Z Python
    tool: ruff
    target: py399
  ac:
    name: A C
    tool: clang-tidy
    std: gnu2x
  mjava:
    name: M Java
    tool: checkstyle
  mgo:
    name: M Go
    tool: go vet
tools:
  ruff: "9.9.9"
`
	r, err := parse([]byte(doc))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	var keys []string
	for _, l := range r.linters {
		keys = append(keys, l.Language())
	}
	if want := []string{"ac", "mgo", "mjava", "zpy"}; !slices.Equal(keys, want) {
		t.Errorf("keys = %v, want %v (sorted)", keys, want)
	}
	if r.version != 1 {
		t.Errorf("version = %d, want 1", r.version)
	}
	if r.tools["ruff"] != "9.9.9" {
		t.Errorf("tools = %v, want the ruff pin", r.tools)
	}
	var cmd []string
	for _, l := range r.linters {
		if l.Language() == "zpy" {
			cmd = l.Command([]string{"f.py"})
		}
	}
	want := []string{"/usr/local/bin/ruff", "check", "--no-cache", "--output-format=json", "--target-version", "py399", "f.py"}
	if !slices.Equal(cmd, want) {
		t.Errorf("zpy command = %v, want %v", cmd, want)
	}
}
