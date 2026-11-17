package main

import (
	"sort"
	"unicode/utf8"
)

// lineIndex converts byte offsets of a text into 1-based lines and
// columns. A column counts code points from the line start.
type lineIndex struct {
	text   string
	starts []int
}

func newLineIndex(text string) *lineIndex {
	starts := []int{0}
	for i := 0; i < len(text); i++ {
		if text[i] == '\n' {
			starts = append(starts, i+1)
		}
	}
	return &lineIndex{text: text, starts: starts}
}

// position returns the line and column of the character at offset.
func (ix *lineIndex) position(offset int) (line, column int) {
	offset = min(max(offset, 0), len(ix.text))
	line = sort.SearchInts(ix.starts, offset+1)
	column = utf8.RuneCountInString(ix.text[ix.starts[line-1]:offset]) + 1
	return line, column
}

// lastPosition returns the line and column of the last character of the
// range [start, end), or of start when the range is empty.
func (ix *lineIndex) lastPosition(start, end int) (line, column int) {
	end = min(end, len(ix.text))
	if end <= start {
		return ix.position(start)
	}
	_, size := utf8.DecodeLastRuneInString(ix.text[start:end])
	return ix.position(end - size)
}

// lineEnd returns the offset of the newline that ends the line holding
// offset, or the end of the text.
func (ix *lineIndex) lineEnd(offset int) int {
	line, _ := ix.position(offset)
	if line < len(ix.starts) {
		return ix.starts[line] - 1
	}
	return len(ix.text)
}

// tokenEnd returns the exclusive end of the token that starts at offset:
// a quoted run, a word, or one character.
func tokenEnd(text string, offset int) int {
	if offset >= len(text) {
		return len(text)
	}
	switch c := text[offset]; {
	case c == '\'' || c == '"' || c == '`':
		return skipQuoted(text, offset)
	case isWordByte(c):
		end := offset
		for end < len(text) && isWordByte(text[end]) {
			end++
		}
		return end
	default:
		_, size := utf8.DecodeRuneInString(text[offset:])
		return offset + size
	}
}

// isWordByte reports whether c can be part of an unquoted word. Bytes of
// a multi-byte character count, since identifiers may hold them.
func isWordByte(c byte) bool {
	return c == '_' || c == '$' || c >= 0x80 ||
		(c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

// skipQuoted returns the offset after the quoted run that opens at
// offset, or the end of the text when the quote is not closed. A doubled
// quote character stays inside the run, and so does a backslash escape
// except in a backtick identifier.
func skipQuoted(text string, offset int) int {
	quote := text[offset]
	for i := offset + 1; i < len(text); i++ {
		switch {
		case text[i] == '\\' && quote != '`':
			i++
		case text[i] == quote:
			if i+1 < len(text) && text[i+1] == quote {
				i++
				continue
			}
			return i + 1
		}
	}
	return len(text)
}
