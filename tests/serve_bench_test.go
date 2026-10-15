//go:build conformance

package tests

import (
	"fmt"
	"net/http"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestServeJavaThroughput measures Java lints end to end through
// dial-stdio against a container started the way core starts it. It is
// opt-in: set COBE_LINT_BENCH=1. CPU is the container cgroup's usage, so
// it includes the server, the checkstyle JVM and the dial-stdio processes.
func TestServeJavaThroughput(t *testing.T) {
	if os.Getenv("COBE_LINT_BENCH") == "" {
		t.Skip("set COBE_LINT_BENCH=1 to run the throughput measurement")
	}
	name := startServe(t, []string{"--network", "none"})
	s := served{client: execClient(name, 4), base: "http://linter"}
	s.waitReady(t)

	req := map[string]any{"language": "java", "filename": "Solution.java", "code": javaDirty}
	phase := func(label string, total, clients int) {
		before := cgroupCPU(t, name)
		var mu sync.Mutex
		var latencies []time.Duration
		var failures int
		start := time.Now()
		var wg sync.WaitGroup
		for c := range clients {
			n := total / clients
			if c < total%clients {
				n++
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				for range n {
					t0 := time.Now()
					status, _ := s.lint(t, req)
					d := time.Since(t0)
					mu.Lock()
					latencies = append(latencies, d)
					if status != http.StatusOK {
						failures++
					}
					mu.Unlock()
				}
			}()
		}
		wg.Wait()
		wall := time.Since(start)
		cpu := cgroupCPU(t, name) - before
		slices.Sort(latencies)
		pct := func(p float64) time.Duration { return latencies[int(p*float64(len(latencies)-1))] }
		t.Logf("%s: %d lints, %d clients, %d failed, wall %v, %.1f lints/s, CPU %.1f ms/lint, p50 %v, p99 %v, max %v",
			label, total, clients, failures, wall.Round(time.Millisecond), float64(total)/wall.Seconds(),
			float64(cpu)/1000/float64(total), pct(0.50).Round(100*time.Microsecond),
			pct(0.99).Round(100*time.Microsecond), latencies[len(latencies)-1].Round(100*time.Microsecond))
		if failures > 0 {
			t.Errorf("%s: %d lints failed", label, failures)
		}
	}
	phase("sequential", 200, 1)
	phase("concurrent", 200, 4)
	phase("sequential again", 200, 1)
}

// cgroupCPU reads the container cgroup's usage_usec.
func cgroupCPU(t *testing.T, container string) int64 {
	t.Helper()
	out, stderr, code := docker(t, "exec", container, "cat", "/sys/fs/cgroup/cpu.stat")
	if code != 0 {
		t.Fatalf("cpu.stat: %s", stderr)
	}
	for _, line := range strings.Split(out, "\n") {
		if v, ok := strings.CutPrefix(line, "usage_usec "); ok {
			n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
			if err != nil {
				t.Fatal(err)
			}
			return n
		}
	}
	t.Fatal(fmt.Sprintf("no usage_usec in cpu.stat:\n%s", out))
	return 0
}
