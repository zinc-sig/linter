package javac

// Inline fixtures: real javac worker output captured by running
// `JavacWorker --once --release 11 Solution.java` in the image's runtime.

const cleanStdout = `{"tool":"javac 21.0.12.1 --release 11","findings":[]}
`

// A Java 16 library call and a type error, in a file indented with tabs.
const dirtyStdout = `{"tool":"javac 21.0.12.1 --release 11","findings":[{"path":"Solution.java","line":5,"column":44,"end_line":5,"end_column":50,"severity":"error","rule":"compiler.err.cant.resolve.location.args","message":"cannot find symbol\n  symbol:   method toList()\n  location: interface java.util.stream.Stream<java.lang.Integer>"},{"path":"Solution.java","line":6,"column":11,"end_line":6,"end_column":13,"severity":"error","rule":"compiler.err.prob.found.req","message":"incompatible types: java.lang.String cannot be converted to int"}]}
`

// A source javac cannot parse to the end is still a finding.
const brokenStdout = `{"tool":"javac 21.0.12.1 --release 11","findings":[{"path":"Solution.java","line":1,"column":24,"severity":"error","rule":"compiler.err.premature.eof","message":"reached end of file while parsing"}]}
`

const crashExitCode = 1

const crashStderr = `javac-worker: java.io.IOException: release 99 is not supported by javac 21.0.12.1 (supported: 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21)
`
