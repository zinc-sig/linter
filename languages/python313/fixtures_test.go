package python313

// Inline fixtures: real native pylint output captured by running the tool in
// the image (workdir /workspace); the *ExitCode consts are the recorded
// exit statuses of those runs.

const cleanExitCode = 0

const cleanStdout = `[]
`

const dirtyExitCode = 4

const dirtyStdout = `[
    {
        "type": "warning",
        "module": "solution",
        "obj": "main",
        "line": 5,
        "column": 4,
        "endLine": 5,
        "endColumn": 10,
        "path": "solution.py",
        "symbol": "unused-variable",
        "message": "Unused variable 'unused'",
        "message-id": "W0612"
    },
    {
        "type": "warning",
        "module": "solution",
        "obj": "",
        "line": 1,
        "column": 0,
        "endLine": 1,
        "endColumn": 9,
        "path": "solution.py",
        "symbol": "unused-import",
        "message": "Unused import os",
        "message-id": "W0611"
    }
]
`

const multifileExitCode = 4

const multifileStdout = `[
    {
        "type": "warning",
        "module": "dirty",
        "obj": "main",
        "line": 5,
        "column": 4,
        "endLine": 5,
        "endColumn": 10,
        "path": "dirty.py",
        "symbol": "unused-variable",
        "message": "Unused variable 'unused'",
        "message-id": "W0612"
    },
    {
        "type": "warning",
        "module": "dirty",
        "obj": "",
        "line": 1,
        "column": 0,
        "endLine": 1,
        "endColumn": 9,
        "path": "dirty.py",
        "symbol": "unused-import",
        "message": "Unused import os",
        "message-id": "W0611"
    }
]
`

const syntaxErrorExitCode = 2

const syntaxErrorStdout = `[
    {
        "type": "error",
        "module": "solution",
        "obj": "",
        "line": 1,
        "column": 12,
        "endLine": null,
        "endColumn": null,
        "path": "solution.py",
        "symbol": "syntax-error",
        "message": "Parsing failed: 'invalid syntax (solution, line 1)'",
        "message-id": "E0001"
    }
]
`

const usageErrorExitCode = 32

const usageErrorStderr = `usage: pylint [options]
pylint: error: Unrecognized option found: no-such-flag
`
