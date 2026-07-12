// Package languages is the explicit registry of language implementations.
// Adding a language to the image means implementing linter.Linter in a new
// languages/<lang> package and appending it to All — nothing else.
package languages

import (
	"github.com/zinc-sig/linter/languages/c"
	"github.com/zinc-sig/linter/languages/cpp"
	"github.com/zinc-sig/linter/languages/golang"
	"github.com/zinc-sig/linter/languages/java"
	"github.com/zinc-sig/linter/languages/python"
	"github.com/zinc-sig/linter/linter"
)

// All returns one implementation per supported language; Linter.Language()
// is the manifest key.
func All() []linter.Linter {
	return []linter.Linter{
		c.New(),
		cpp.New(),
		golang.New(),
		java.New(),
		python.New(),
	}
}
