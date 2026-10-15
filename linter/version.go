package linter

import (
	"os/exec"
	"regexp"
	"strings"
	"sync"
)

// versions memoizes ToolVersion per probe: the tools are fixed for the
// life of the process, so each one is probed once.
var versions sync.Map

// ToolVersion best-effort probes a native tool's version for the Report's
// freeform tool string: it runs argv, matches pattern (whose first capture
// group is the version) against the combined output, and returns
// "name version". It never fails — on any problem it returns name alone.
// Each probe runs once per process, with the scrubbed ToolEnv rather than
// the caller's environment.
func ToolVersion(name, pattern string, argv ...string) string {
	key := name + "\x00" + pattern + "\x00" + strings.Join(argv, "\x00")
	if v, ok := versions.Load(key); ok {
		return v.(string)
	}
	v := probeVersion(name, pattern, argv)
	versions.Store(key, v)
	return v
}

func probeVersion(name, pattern string, argv []string) string {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return name
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Env = ToolEnv()
	out, _ := cmd.CombinedOutput()
	if m := re.FindSubmatch(out); len(m) > 1 {
		return name + " " + string(m[1])
	}
	return name
}
