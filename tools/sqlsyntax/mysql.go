package main

import (
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/pingcap/tidb/pkg/parser"
	// The parser needs a value-expression driver to build literals.
	_ "github.com/pingcap/tidb/pkg/parser/test_driver"
)

// The MySQL check parses each statement with the TiDB parser, which
// follows the MySQL 8.0 grammar but is not the server's own parser. It
// rejects some statements the server accepts, so an error is reported
// only for a statement of a kind where the two agree:
//
//   - stored programs (CREATE PROCEDURE, FUNCTION, TRIGGER, EVENT) are not
//     checked, and after one written without a DELIMITER command nothing
//     further is checked, because its body cannot be told from the
//     statements that follow it;
//   - statements that begin with a keyword in uncheckedLeads are not
//     checked;
//   - CREATE, ALTER and DROP are checked for the objects in
//     checkedObjects only;
//   - an error in a statement that uses a construct in unsupported is
//     dropped.
//
// A statement that begins with a word MySQL does not know is checked, so
// a misspelled first keyword is reported.

// uncheckedLeads are the keywords that begin a MySQL statement the check
// does not judge: statements the TiDB parser lacks, and the statements
// that only occur inside a stored program body.
var uncheckedLeads = wordSet(
	"BINLOG", "CACHE", "CHANGE", "CHECK", "CHECKSUM", "CLONE", "GET",
	"HANDLER", "HELP", "IMPORT", "INSTALL", "KILL", "LOAD", "PURGE",
	"REPAIR", "RESET", "RESIGNAL", "RESTART", "SHUTDOWN", "SIGNAL", "STOP",
	"UNINSTALL", "XA",
	"CASE", "CLOSE", "DECLARE", "END", "FETCH", "IF", "ITERATE", "LEAVE",
	"LOOP", "OPEN", "REPEAT", "RETURN", "WHILE",
)

// definitionLeads begin the statements whose object kind decides whether
// they are checked.
var definitionLeads = wordSet("CREATE", "ALTER", "DROP")

// checkedObjects are the object kinds whose CREATE, ALTER and DROP
// statements are checked.
var checkedObjects = wordSet("TABLE", "INDEX", "VIEW", "DATABASE", "SCHEMA", "USER", "ROLE")

// programObjects are the stored program kinds.
var programObjects = wordSet("PROCEDURE", "FUNCTION", "TRIGGER", "EVENT")

// uncheckedObjects are the other object kinds MySQL defines.
var uncheckedObjects = wordSet("TABLESPACE", "SERVER", "LOGFILE", "RESOURCE", "INSTANCE", "UNDO", "REFERENCE")

// definitionModifiers are the words that may stand between CREATE, ALTER
// or DROP and the object kind.
var definitionModifiers = wordSet(
	"OR", "REPLACE", "TEMPORARY", "UNIQUE", "FULLTEXT", "SPATIAL", "ONLINE",
	"OFFLINE", "ALGORITHM", "UNDEFINED", "MERGE", "TEMPTABLE", "DEFINER",
	"CURRENT_USER", "SQL", "SECURITY", "INVOKER", "AGGREGATE", "IF", "NOT",
	"EXISTS",
)

// unsupported matches, in a statement stripped of strings and comments,
// the constructs the server accepts and the TiDB parser does not: SELECT
// INTO a user variable, and JSON_TABLE.
var unsupported = regexp.MustCompile(`(?i)\bINTO\s+@|\bJSON_TABLE\b`)

func wordSet(words ...string) map[string]bool {
	set := make(map[string]bool, len(words))
	for _, w := range words {
		set[w] = true
	}
	return set
}

// statement is one statement of a file: its text without the delimiter
// and the byte offset of that text in the file.
type statement struct {
	start int
	text  string
	// delimited is true when a DELIMITER command had replaced the
	// semicolon, so the text holds a whole compound statement.
	delimited bool
}

func checkMySQL(sql string) []problem {
	var problems []problem
	p := parser.New()
	for _, stmt := range splitMySQL(sql) {
		verdict := classify(stmt.text)
		if verdict == program && !stmt.delimited {
			break
		}
		if verdict != checked {
			continue
		}
		if _, _, err := p.Parse(stmt.text, "", ""); err != nil {
			if unsupported.MatchString(stripQuotedAndComments(stmt.text)) {
				continue
			}
			offset := errorOffset(stmt.text, err.Error())
			near := nearText(stmt.text[offset:])
			message := "syntax error at the end of the statement"
			if near != "" {
				message = "syntax error near " + strconv.Quote(near)
			}
			problems = append(problems, problem{
				start:   stmt.start + offset,
				end:     stmt.start + tokenEnd(stmt.text, offset),
				message: message,
			})
		}
	}
	return problems
}

// splitMySQL cuts a file into statements the way the mysql client does:
// at the delimiter outside quotes and comments, honoring DELIMITER
// commands. Statements that hold nothing but whitespace and comments are
// left out.
func splitMySQL(sql string) []statement {
	var statements []statement
	delimiter := ";"
	start := 0
	atStart := true
	flush := func(end int) {
		if !atStart {
			statements = append(statements, statement{start: start, text: sql[start:end], delimited: delimiter != ";"})
		}
	}
	for i := 0; i < len(sql); {
		c := sql[i]
		switch {
		case c == '\'' || c == '"' || c == '`':
			i = skipQuoted(sql, i)
			atStart = false
		case c == '#' || isDashComment(sql, i):
			i = lineEnd(sql, i)
		case strings.HasPrefix(sql[i:], "/*"):
			// A version comment holds statement text the server runs.
			if strings.HasPrefix(sql[i:], "/*!") {
				atStart = false
			}
			i = blockCommentEnd(sql, i)
		case strings.HasPrefix(sql[i:], delimiter):
			flush(i)
			i += len(delimiter)
			start, atStart = i, true
		case atStart && isDelimiterCommand(sql, i):
			end := lineEnd(sql, i)
			if fields := strings.Fields(sql[i+len("delimiter") : end]); len(fields) > 0 {
				delimiter = fields[0]
			}
			i = end
			start = i
		default:
			if !isSpace(c) {
				atStart = false
			}
			i++
		}
	}
	flush(len(sql))
	return statements
}

func isSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f' || c == '\v'
}

// isDashComment reports whether a "-- " comment starts at i. MySQL needs
// whitespace or the end of the text after the two dashes.
func isDashComment(sql string, i int) bool {
	if !strings.HasPrefix(sql[i:], "--") {
		return false
	}
	return i+2 == len(sql) || isSpace(sql[i+2])
}

// lineEnd returns the offset of the newline that ends the line holding
// i, or the end of the text.
func lineEnd(sql string, i int) int {
	if n := strings.IndexByte(sql[i:], '\n'); n >= 0 {
		return i + n
	}
	return len(sql)
}

// blockCommentEnd returns the offset after the block comment that opens
// at i, or the end of the text when it is not closed.
func blockCommentEnd(sql string, i int) int {
	if n := strings.Index(sql[i+2:], "*/"); n >= 0 {
		return i + 2 + n + 2
	}
	return len(sql)
}

func isDelimiterCommand(sql string, i int) bool {
	const word = "delimiter"
	if len(sql)-i <= len(word) || !strings.EqualFold(sql[i:i+len(word)], word) {
		return false
	}
	return sql[i+len(word)] == ' ' || sql[i+len(word)] == '\t'
}

type verdict int

const (
	checked verdict = iota
	unchecked
	program
)

// classify decides from the leading words of a statement whether the
// check judges it.
func classify(stmt string) verdict {
	words, versioned := leadingWords(stmt, 12)
	if versioned || len(words) == 0 {
		return unchecked
	}
	lead := words[0]
	if uncheckedLeads[lead] {
		return unchecked
	}
	if !definitionLeads[lead] {
		return checked
	}
	for _, w := range words[1:] {
		switch {
		case checkedObjects[w]:
			return checked
		case programObjects[w]:
			if lead == "CREATE" {
				return program
			}
			return unchecked
		case uncheckedObjects[w]:
			return unchecked
		}
	}
	// No object kind was found. A known modifier after the lead means a
	// form this list does not cover; any other word is not MySQL.
	if len(words) > 1 && definitionModifiers[words[1]] {
		return unchecked
	}
	return checked
}

// leadingWords returns up to limit upper-cased words from the start of a
// statement, skipping whitespace, comments, quoted runs and punctuation,
// and stopping at the first parenthesis. A statement that opens with a
// parenthesis yields the one word "(". versioned is true when a version
// comment precedes the words collected.
func leadingWords(stmt string, limit int) (words []string, versioned bool) {
	for i := 0; i < len(stmt) && len(words) < limit; {
		c := stmt[i]
		switch {
		case c == '\'' || c == '"' || c == '`':
			i = skipQuoted(stmt, i)
		case c == '#' || isDashComment(stmt, i):
			i = lineEnd(stmt, i)
		case strings.HasPrefix(stmt[i:], "/*"):
			if strings.HasPrefix(stmt[i:], "/*!") {
				return words, true
			}
			i = blockCommentEnd(stmt, i)
		case c == '(':
			if len(words) == 0 {
				return []string{"("}, false
			}
			return words, false
		case isWordByte(c):
			end := tokenEnd(stmt, i)
			words = append(words, strings.ToUpper(stmt[i:end]))
			i = end
		default:
			i++
		}
	}
	return words, false
}

// stripQuotedAndComments returns a statement with every quoted run and
// comment replaced by a space.
func stripQuotedAndComments(stmt string) string {
	var b strings.Builder
	for i := 0; i < len(stmt); {
		c := stmt[i]
		switch {
		case c == '\'' || c == '"' || c == '`':
			i = skipQuoted(stmt, i)
			b.WriteByte(' ')
		case c == '#' || isDashComment(stmt, i):
			i = lineEnd(stmt, i)
			b.WriteByte(' ')
		case strings.HasPrefix(stmt[i:], "/*"):
			i = blockCommentEnd(stmt, i)
			b.WriteByte(' ')
		default:
			b.WriteByte(c)
			i++
		}
	}
	return b.String()
}

var (
	nearRE        = regexp.MustCompile(`line \d+ column \d+ near "`)
	totalLengthRE = regexp.MustCompile(`\(total length (\d+)\)\s*$`)
)

// errorOffset recovers, from a TiDB parser error, the byte offset in the
// statement of the token the parser stopped at. The message quotes the
// statement from that token to its end, cut to 2048 bytes with the full
// length appended when it is longer. An error without that shape is
// placed at the start of the statement.
func errorOffset(stmt, message string) int {
	loc := nearRE.FindStringIndex(message)
	if loc == nil {
		return leadingOffset(stmt)
	}
	quoted := message[loc[1]:]
	if m := totalLengthRE.FindStringSubmatch(quoted); m != nil {
		if n, err := strconv.Atoi(m[1]); err == nil && n <= len(stmt) {
			return len(stmt) - n
		}
	}
	for offset := 0; offset <= len(stmt); offset++ {
		rest := stmt[offset:]
		if strings.HasPrefix(quoted, rest) && strings.HasPrefix(quoted[len(rest):], `"`) {
			return offset
		}
	}
	return leadingOffset(stmt)
}

// leadingOffset returns the offset of the first character of a statement
// that is not whitespace.
func leadingOffset(stmt string) int {
	return len(stmt) - len(strings.TrimLeft(stmt, " \t\r\n\f\v"))
}

// nearText returns the start of rest for an error message: its first
// line, trimmed, cut to 40 code points.
func nearText(rest string) string {
	rest = strings.TrimSpace(rest)
	if n := strings.IndexAny(rest, "\r\n"); n >= 0 {
		rest = rest[:n]
	}
	const limit = 40
	if utf8.RuneCountInString(rest) <= limit {
		return rest
	}
	end := 0
	for count := 0; count < limit; count++ {
		_, size := utf8.DecodeRuneInString(rest[end:])
		end += size
	}
	return rest[:end] + "..."
}
