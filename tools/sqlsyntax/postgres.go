package main

import (
	"errors"
	"regexp"
	"strconv"
	"unicode/utf8"

	pg_query "github.com/pganalyze/pg_query_go/v6"
	"github.com/pganalyze/pg_query_go/v6/parser"
)

// checkPostgreSQL parses the text with PostgreSQL's own parser. The
// parser stops at the first error, so after each error the check resumes
// at the statement that follows and reports one error per statement.
func checkPostgreSQL(sql string) []problem {
	var problems []problem
	for base := 0; base < len(sql); {
		rest := sql[base:]
		_, err := pg_query.Parse(rest)
		if err == nil {
			break
		}
		var perr *parser.Error
		if !errors.As(err, &perr) {
			problems = append(problems, problem{start: base, end: base, message: err.Error()})
			break
		}
		offset := cursorOffset(rest, perr.Cursorpos)
		tokens := scanTokens(rest)
		problems = append(problems, problem{
			start:   base + offset,
			end:     base + tokenEndAt(rest, tokens, offset),
			message: shortenNear(perr.Message),
		})
		next, ok := nextStatement(tokens, offset)
		if !ok {
			break
		}
		base += next
	}
	return problems
}

// cursorOffset converts a parser cursor position, a 1-based character
// index, into a byte offset. A position of zero means the parser gave
// none, and maps to the start.
func cursorOffset(text string, cursor int) int {
	offset := 0
	for i := 1; i < cursor && offset < len(text); i++ {
		_, size := utf8.DecodeRuneInString(text[offset:])
		offset += size
	}
	return offset
}

// scanTokens returns the tokens of a text, or nil when the scanner
// itself fails, as it does on an unterminated string or comment.
func scanTokens(text string) []*pg_query.ScanToken {
	result, err := pg_query.Scan(text)
	if err != nil {
		return nil
	}
	return result.Tokens
}

// tokenEndAt returns the exclusive end of the token that starts at
// offset, falling back to a lexical guess when the scanner has no token
// there.
func tokenEndAt(text string, tokens []*pg_query.ScanToken, offset int) int {
	for _, t := range tokens {
		if int(t.Start) == offset {
			return int(t.End)
		}
	}
	return tokenEnd(text, offset)
}

// nextStatement returns the offset after the semicolon that ends the
// statement holding the error at offset. It reports false when no
// statement can be told to follow: the scanner failed, a parenthesis is
// left open, or the statement holds a BEGIN ATOMIC body, whose inner
// semicolons do not end it.
func nextStatement(tokens []*pg_query.ScanToken, offset int) (int, bool) {
	depth := 0
	atomic := false
	for _, t := range tokens {
		switch t.Token {
		case pg_query.Token_ASCII_40:
			depth++
		case pg_query.Token_ASCII_41:
			if depth > 0 {
				depth--
			}
		case pg_query.Token_ATOMIC:
			atomic = true
		case pg_query.Token_ASCII_59:
			if depth > 0 {
				continue
			}
			if int(t.Start) >= offset {
				return int(t.End), !atomic
			}
			atomic = false
		}
	}
	return 0, false
}

var atOrNearRE = regexp.MustCompile(`(?s)^(.* at or near )"(.*)"$`)

// shortenNear cuts the text a parser message quotes after "at or near"
// to the start of its first line. The parser quotes everything up to the
// end of the input for an unterminated string or comment.
func shortenNear(message string) string {
	m := atOrNearRE.FindStringSubmatch(message)
	if m == nil {
		return message
	}
	near := nearText(m[2])
	if near == m[2] {
		return message
	}
	return m[1] + strconv.Quote(near)
}
