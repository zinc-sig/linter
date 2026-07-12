package languages

import (
	"slices"
	"testing"
)

func TestRegistry(t *testing.T) {
	all := All()
	var keys []string
	for _, l := range all {
		keys = append(keys, l.Language())
		argv := l.Command([]string{"input-file"})
		if len(argv) == 0 {
			t.Errorf("%s: empty Command", l.Language())
		}
		if !slices.Contains(argv, "input-file") {
			t.Errorf("%s: Command %v does not include the input file", l.Language(), argv)
		}
	}
	slices.Sort(keys)
	want := []string{"c", "cpp", "go", "java", "python"}
	if !slices.Equal(keys, want) {
		t.Errorf("registry keys = %v, want %v", keys, want)
	}
}
