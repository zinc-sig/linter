// Package java lints Java sources with Checkstyle.
package java

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"strconv"
	"strings"

	"github.com/zinc-sig/linter/linter"
)

// CheckstyleVersion is the Checkstyle release baked into the image;
// cmd/toolversions feeds it to the Dockerfile build. Its grammar determines
// the Java language level accepted (10.21.1 parses source up to Java 21); it
// runs on the image's default-jre-headless (OpenJDK 21 on Debian 13).
const CheckstyleVersion = "10.21.1"

const (
	// javaPath is the jlink'ed minimal runtime built for the checkstyle
	// jar (see the Dockerfile's jre stage) — the image ships no full JRE.
	javaPath   = "/opt/java/bin/java"
	jarPath    = "/opt/checkstyle.jar"
	configPath = "/opt/checkstyle-config.xml"
)

var severityByName = map[string]string{
	"error":   linter.SeverityError,
	"warning": linter.SeverityWarning,
	"info":    linter.SeverityInfo,
	"ignore":  linter.SeverityInfo,
}

type xmlReport struct {
	XMLName xml.Name  `xml:"checkstyle"`
	Version string    `xml:"version,attr"`
	Files   []xmlFile `xml:"file"`
}

type xmlFile struct {
	Name   string     `xml:"name,attr"`
	Errors []xmlError `xml:"error"`
}

type xmlError struct {
	Line     string `xml:"line,attr"`
	Column   string `xml:"column,attr"`
	Severity string `xml:"severity,attr"`
	Message  string `xml:"message,attr"`
	Source   string `xml:"source,attr"`
}

type checkstyle struct{}

// New returns the java language implementation.
func New() linter.Linter { return checkstyle{} }

func (checkstyle) Language() string { return "java" }

// Name is the display name served to UI/API surfaces.
func (checkstyle) Name() string { return "Java" }

func (checkstyle) Command(files []string) []string {
	return append([]string{javaPath, "-jar", jarPath, "-c", configPath, "-f", "xml"}, files...)
}

func (checkstyle) Parse(stdout, stderr []byte, exitCode int) (linter.Report, error) {
	// Checkstyle exits with the number of violations it found; the XML on
	// stdout is the data. On a source it cannot parse at all it throws and
	// emits no XML — an operational failure (contract §3). Isolate the
	// document defensively in case audit chatter surrounds it.
	start := bytes.Index(stdout, []byte("<?xml"))
	end := bytes.LastIndex(stdout, []byte("</checkstyle>"))
	if start < 0 || end < 0 {
		return linter.Report{}, fmt.Errorf("checkstyle: no XML report in output (exit %d)\nstderr: %s", exitCode, linter.StderrSnippet(stderr))
	}
	var doc xmlReport
	if err := xml.Unmarshal(stdout[start:end+len("</checkstyle>")], &doc); err != nil {
		return linter.Report{}, fmt.Errorf("checkstyle: unparseable XML output: %v", err)
	}

	findings := []linter.Finding{}
	for _, file := range doc.Files {
		for _, e := range file.Errors {
			severity, ok := severityByName[strings.ToLower(e.Severity)]
			if !ok {
				severity = linter.SeverityWarning
			}
			findings = append(findings, linter.Finding{
				Path:     file.Name,
				Line:     atoiAtLeast0(e.Line),
				Column:   atoiAtLeast0(e.Column),
				Severity: severity,
				Rule:     ruleFromSource(e.Source),
				Message:  e.Message,
			})
		}
	}

	tool := "checkstyle"
	if doc.Version != "" {
		tool += " " + doc.Version
	}
	return linter.Report{
		Version:  linter.ReportVersion,
		Language: "java",
		Tool:     tool,
		Findings: findings,
	}, nil
}

// ruleFromSource shortens a fully-qualified module name like
// com.puppycrawl.tools.checkstyle.checks.imports.AvoidStarImportCheck
// to the id "AvoidStarImport" used in checkstyle documentation.
func ruleFromSource(source string) string {
	if source == "" {
		return ""
	}
	rule := source[strings.LastIndexByte(source, '.')+1:]
	return strings.TrimSuffix(rule, "Check")
}

func atoiAtLeast0(s string) int {
	n, err := strconv.Atoi(s)
	if err != nil || n < 0 {
		return 0
	}
	return n
}
