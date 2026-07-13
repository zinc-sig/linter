package python312

// Inline fixtures: real pylint output captured by running
// /opt/python/3.12.13/bin/pylint in the image (workdir /workspace); the
// *ExitCode consts are the recorded exit statuses of those runs.

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
