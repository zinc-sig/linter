// Package languages is the explicit registry of language implementations.
// Adding a language to the image means implementing linter.Linter in a new
// languages/<lang> package and appending it to All — nothing else.
package languages

import (
	"github.com/zinc-sig/linter/languages/c"
	"github.com/zinc-sig/linter/languages/cpp11"
	"github.com/zinc-sig/linter/languages/cpp14"
	"github.com/zinc-sig/linter/languages/golang"
	"github.com/zinc-sig/linter/languages/java"
	"github.com/zinc-sig/linter/languages/python312"
	"github.com/zinc-sig/linter/languages/python313"
	"github.com/zinc-sig/linter/linter"
)

// All returns one implementation per supported language; Linter.Language()
// is the manifest key.
func All() []linter.Linter {
	return []linter.Linter{
		c.New(),
		cpp11.New(),
		cpp14.New(),
		golang.New(),
		java.New(),
		python312.New(),
		python313.New(),
	}
}
