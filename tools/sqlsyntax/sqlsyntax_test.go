package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var caseRE = regexp.MustCompile(`(?m)^-- case: (\S+)\n`)

// loadCases reads a fixture of statements grouped under "-- case:" lines.
func loadCases(t *testing.T, name string) map[string]string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	marks := caseRE.FindAllStringSubmatchIndex(text, -1)
	cases := make(map[string]string, len(marks))
	for i, m := range marks {
		end := len(text)
		if i+1 < len(marks) {
			end = marks[i+1][0]
		}
		cases[text[m[2]:m[3]]] = text[m[1]:end]
	}
	if len(cases) == 0 {
		t.Fatalf("%s holds no cases", name)
	}
	return cases
}

// The fixtures hold each server's own verdicts: a statement it accepts
// must draw no error from the check.
func TestAcceptedStatementsDrawNoError(t *testing.T) {
	for dialect, check := range checkers {
		for name, sql := range loadCases(t, dialect+"-accepted.sql") {
			if problems := check(sql); len(problems) > 0 {
				t.Errorf("%s/%s: reported %q for a statement the server accepts", dialect, name, problems[0].message)
			}
		}
	}
}

// mysqlMisses are the rejected fixtures the MySQL check accepts, because
// the TiDB parser carries the syntax as an extension of its own.
var mysqlMisses = map[string]bool{
	"fetch-first":     true,
	"full-outer-join": true,
	"ilike":           true,
	"pg-returning":    true,
}

func TestRejectedStatementsDrawAnError(t *testing.T) {
	for dialect, check := range checkers {
		for name, sql := range loadCases(t, dialect+"-rejected.sql") {
			problems := check(sql)
			missed := dialect == "mysql" && mysqlMisses[name]
			switch {
			case len(problems) == 0 && !missed:
				t.Errorf("%s/%s: no error for a statement the server rejects", dialect, name)
			case len(problems) > 0 && missed:
				t.Errorf("%s/%s: now reported; drop it from mysqlMisses", dialect, name)
			}
		}
	}
}

type span struct {
	line, column, endLine, endColumn int
	message                          string
}

// diagnose runs the command on one file and returns its diagnostics.
func diagnose(t *testing.T, dialect, sql string) []span {
	t.Helper()
	path := filepath.Join(t.TempDir(), "answer.sql")
	if err := os.WriteFile(path, []byte(sql), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if code := run([]string{"--dialect", dialect, path}, &stdout, &stderr); code != 0 && code != 1 {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	var doc document
	if err := json.Unmarshal(stdout.Bytes(), &doc); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, stdout.String())
	}
	spans := make([]span, 0, len(doc.Diagnostics))
	for _, d := range doc.Diagnostics {
		if d.File != path {
			t.Errorf("file = %q, want %q", d.File, path)
		}
		spans = append(spans, span{d.Line, d.Column, d.EndLine, d.EndColumn, d.Message})
	}
	return spans
}

func expectSpans(t *testing.T, got, want []span) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("diagnostics = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("diagnostic %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestMySQLReportsEachBadStatement(t *testing.T) {
	sql := "select id form student;\n" +
		"select * from student;\n" +
		"  update student name = 'x' where id = 1;\n" +
		"select * from student limit;\n"
	expectSpans(t, diagnose(t, "mysql", sql), []span{
		{1, 16, 1, 22, `syntax error near "student"`},
		{3, 23, 3, 23, `syntax error near "= 'x' where id = 1"`},
		{4, 28, 4, 28, "syntax error at the end of the statement"},
	})
}

func TestMySQLColumnsCountCodePoints(t *testing.T) {
	expectSpans(t, diagnose(t, "mysql", "select 'ünï' frm student;"), []span{
		{1, 18, 1, 24, `syntax error near "student"`},
	})
}

func TestMySQLMisspelledFirstKeyword(t *testing.T) {
	expectSpans(t, diagnose(t, "mysql", "selec id from student;"), []span{
		{1, 1, 1, 5, `syntax error near "selec id from student"`},
	})
	expectSpans(t, diagnose(t, "mysql", "create tabel t (a int);"), []span{
		{1, 8, 1, 12, `syntax error near "tabel t (a int)"`},
	})
}

func TestMySQLUnterminatedStringMarksItsFirstLine(t *testing.T) {
	expectSpans(t, diagnose(t, "mysql", "select 'abc from student;\nselect 2;\n"), []span{
		{1, 8, 1, 25, `syntax error near "'abc from student;"`},
	})
}

func TestMySQLLeavesUnjudgedStatementsAlone(t *testing.T) {
	for name, sql := range map[string]string{
		"statement the parser lacks":  "handler student opn;",
		"program body statement":      "declare n int default;",
		"drop of a stored program":    "drop trigger if exists;",
		"object kind outside the set": "create tablespace ts add;",
		"version comment":             "/*!50001 create view v as selec 1 */;",
		"select into a user variable": "select count(*) into @c from student where;",
		"json_table":                  "select * from json_table('[]', '$[*]' columns (a int path '$')) t where;",
	} {
		if got := diagnose(t, "mysql", sql); len(got) != 0 {
			t.Errorf("%s: reported %+v", name, got)
		}
	}
}

func TestMySQLStopsAtAStoredProgram(t *testing.T) {
	sql := "selec 1;\n" +
		"create trigger cap before insert on enrollment for each row\n" +
		"begin\n" +
		"  if new.grade > 100 then set new.grade = 100; end if;\n" +
		"end;\n" +
		"selec 2;\n"
	expectSpans(t, diagnose(t, "mysql", sql), []span{
		{1, 1, 1, 5, `syntax error near "selec 1"`},
	})
}

func TestMySQLChecksPastADelimitedProgram(t *testing.T) {
	sql := "delimiter //\n" +
		"create procedure bump(in cid int)\n" +
		"begin\n" +
		"  update enrollment set grade = grade + 1 where course_id = cid;\n" +
		"end//\n" +
		"delimiter ;\n" +
		"selec 2;\n"
	expectSpans(t, diagnose(t, "mysql", sql), []span{
		{7, 1, 7, 5, `syntax error near "selec 2"`},
	})
}

func TestSplitMySQL(t *testing.T) {
	for name, tc := range map[string]struct {
		sql  string
		want []string
	}{
		"semicolons":            {"select 1; select 2 ;select 3", []string{"select 1", " select 2 ", "select 3"}},
		"quoted semicolons":     {"select ';', \";\", `;`; select 2", []string{"select ';', \";\", `;`", " select 2"}},
		"escaped quote":         {`select 'a\'; b'; select 2`, []string{`select 'a\'; b'`, " select 2"}},
		"doubled quote":         {"select 'a''; b'; select 2", []string{"select 'a''; b'", " select 2"}},
		"comments":              {"select 1 -- a; b\n; # c; d\nselect 2 /* e; f */;", []string{"select 1 -- a; b\n", " # c; d\nselect 2 /* e; f */"}},
		"dashes without space":  {"select 5--3; select 2", []string{"select 5--3", " select 2"}},
		"empty statements":      {";; -- only a comment\n; select 1;", []string{" select 1"}},
		"delimiter command":     {"delimiter $$\nselect 1; select 2$$\nDELIMITER ;\nselect 3;", []string{"\nselect 1; select 2", "\nselect 3"}},
		"unterminated string":   {"select 'a; select 2", []string{"select 'a; select 2"}},
		"version comment alone": {"/*!40101 set names utf8 */;", []string{"/*!40101 set names utf8 */"}},
	} {
		var got []string
		for _, s := range splitMySQL(tc.sql) {
			got = append(got, s.text)
			if tc.sql[s.start:s.start+len(s.text)] != s.text {
				t.Errorf("%s: statement %q does not sit at offset %d", name, s.text, s.start)
			}
		}
		if strings.Join(got, "|") != strings.Join(tc.want, "|") {
			t.Errorf("%s: statements = %q, want %q", name, got, tc.want)
		}
	}
}

func TestPostgreSQLReportsOneErrorPerStatement(t *testing.T) {
	sql := "select id form student;\n" +
		"select * from student;\n" +
		"update student name = 'x' where id = 1;\n" +
		"select 'ünï' as a, grade:::int from enrollment;\n"
	expectSpans(t, diagnose(t, "postgresql", sql), []span{
		{1, 16, 1, 22, `syntax error at or near "student"`},
		{3, 21, 3, 21, `syntax error at or near "="`},
		{4, 27, 4, 27, `syntax error at or near ":"`},
	})
}

func TestPostgreSQLStopsWhereNoStatementCanFollow(t *testing.T) {
	for name, tc := range map[string]struct {
		sql  string
		want []span
	}{
		"open parenthesis": {
			"select count(* from student;\nselec 2;\n",
			[]span{{1, 16, 1, 19, `syntax error at or near "from"`}},
		},
		"unterminated string": {
			"select 'abc from student;\nselec 2;\n",
			[]span{{1, 8, 1, 25, `unterminated quoted string at or near "'abc from student;"`}},
		},
		"atomic body": {
			"create function two() returns int language sql begin atomic select 1 select 2; end;\nselec 3;\n",
			[]span{{1, 77, 1, 77, `syntax error at or near "2"`}},
		},
	} {
		t.Run(name, func(t *testing.T) {
			expectSpans(t, diagnose(t, "postgresql", tc.sql), tc.want)
		})
	}
}

func TestPostgreSQLDollarQuotedBody(t *testing.T) {
	sql := "create function f() returns int language plpgsql as $$\nbegin\n  return 1;\nend;\n$$;\nselec 2;\n"
	expectSpans(t, diagnose(t, "postgresql", sql), []span{
		{6, 1, 6, 5, `syntax error at or near "selec"`},
	})
}

func TestEmptyAndCommentOnlyFilesAreClean(t *testing.T) {
	for dialect := range checkers {
		for _, sql := range []string{"", "\n\n", "-- nothing here\n", "/* nor here */"} {
			if got := diagnose(t, dialect, sql); len(got) != 0 {
				t.Errorf("%s %q: reported %+v", dialect, sql, got)
			}
		}
	}
}

func TestRunExitCodes(t *testing.T) {
	dir := t.TempDir()
	clean := filepath.Join(dir, "clean.sql")
	dirty := filepath.Join(dir, "dirty.sql")
	for path, sql := range map[string]string{clean: "select 1;", dirty: "selec 1;"} {
		if err := os.WriteFile(path, []byte(sql), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for name, tc := range map[string]struct {
		args []string
		want int
	}{
		"clean file":       {[]string{"--dialect", "mysql", clean}, 0},
		"dirty file":       {[]string{"--dialect", "postgresql", dirty}, 1},
		"several files":    {[]string{"--dialect", "mysql", clean, dirty}, 1},
		"unknown dialect":  {[]string{"--dialect", "oracle", clean}, 2},
		"no dialect":       {[]string{clean}, 2},
		"no files":         {[]string{"--dialect", "mysql"}, 2},
		"unreadable file":  {[]string{"--dialect", "mysql", filepath.Join(dir, "absent.sql")}, 2},
		"unknown flag":     {[]string{"--nope"}, 2},
		"version":          {[]string{"--version"}, 0},
		"version and file": {[]string{"--version", "--dialect", "mysql", clean}, 0},
	} {
		var stdout, stderr bytes.Buffer
		if got := run(tc.args, &stdout, &stderr); got != tc.want {
			t.Errorf("%s: exit %d, want %d (stderr %q)", name, got, tc.want, stderr.String())
		}
	}
}

func TestCleanRunPrintsAnEmptyList(t *testing.T) {
	path := filepath.Join(t.TempDir(), "clean.sql")
	if err := os.WriteFile(path, []byte("select 1;"), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	run([]string{"--dialect", "mysql", path}, &stdout, &stderr)
	if got := strings.TrimSpace(stdout.String()); got != `{"diagnostics":[]}` {
		t.Errorf("output = %s", got)
	}
}

func TestLineIndex(t *testing.T) {
	ix := newLineIndex("ab\nçd\n\nxyz")
	for offset, want := range map[int][2]int{
		0: {1, 1}, 1: {1, 2}, 2: {1, 3}, 3: {2, 1}, 5: {2, 2}, 6: {2, 3}, 7: {3, 1}, 8: {4, 1}, 11: {4, 4},
	} {
		line, column := ix.position(offset)
		if line != want[0] || column != want[1] {
			t.Errorf("position(%d) = %d:%d, want %d:%d", offset, line, column, want[0], want[1])
		}
	}
	if line, column := ix.lastPosition(3, 6); line != 2 || column != 2 {
		t.Errorf("lastPosition(3, 6) = %d:%d, want 2:2", line, column)
	}
	if got := ix.lineEnd(3); got != 6 {
		t.Errorf("lineEnd(3) = %d, want 6", got)
	}
	if got := ix.lineEnd(9); got != 11 {
		t.Errorf("lineEnd(9) = %d, want 11", got)
	}
}
