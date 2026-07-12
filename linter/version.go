package linter

import (
	"os/exec"
	"regexp"
)

// ToolVersion best-effort probes a native tool's version for the Report's
// freeform tool string: it runs argv, matches pattern (whose first capture
// group is the version) against the combined output, and returns
// "name version". It never fails — on any problem it returns name alone.
func ToolVersion(name, pattern string, argv ...string) string {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return name
	}
	out, _ := exec.Command(argv[0], argv[1:]...).CombinedOutput()
	if m := re.FindSubmatch(out); len(m) > 1 {
		return name + " " + string(m[1])
	}
	return name
}
