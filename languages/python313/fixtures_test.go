package python313

// Inline fixtures: real ruff 0.15.21 output captured by running
// /usr/local/bin/ruff check --no-cache --output-format=json
// --target-version py313 in the image (workdir /workspace); the *ExitCode
// consts are the recorded exit statuses of those runs.

const cleanExitCode = 0

const cleanStdout = `[]`

const dirtyExitCode = 1

const dirtyStdout = `[
  {
    "cell": null,
    "code": "F401",
    "end_location": {
      "column": 10,
      "row": 1
    },
    "filename": "/workspace/solution.py",
    "fix": {
      "applicability": "safe",
      "edits": [
        {
          "content": "",
          "end_location": {
            "column": 1,
            "row": 2
          },
          "location": {
            "column": 1,
            "row": 1
          }
        }
      ],
      "message": "Remove unused import: ` + "`os`" + `"
    },
    "location": {
      "column": 8,
      "row": 1
    },
    "message": "` + "`os`" + ` imported but unused",
    "name": "unused-import",
    "noqa_row": 1,
    "severity": "error",
    "url": "https://docs.astral.sh/ruff/rules/unused-import"
  },
  {
    "cell": null,
    "code": "F821",
    "end_location": {
      "column": 25,
      "row": 5
    },
    "filename": "/workspace/solution.py",
    "fix": null,
    "location": {
      "column": 11,
      "row": 5
    },
    "message": "Undefined name ` + "`undefined_name`" + `",
    "name": "undefined-name",
    "noqa_row": 5,
    "severity": "error",
    "url": "https://docs.astral.sh/ruff/rules/undefined-name"
  }
]`

const multifileExitCode = 1

const multifileStdout = `[
  {
    "cell": null,
    "code": "F401",
    "end_location": {
      "column": 10,
      "row": 1
    },
    "filename": "/workspace/dirty.py",
    "fix": {
      "applicability": "safe",
      "edits": [
        {
          "content": "",
          "end_location": {
            "column": 1,
            "row": 2
          },
          "location": {
            "column": 1,
            "row": 1
          }
        }
      ],
      "message": "Remove unused import: ` + "`os`" + `"
    },
    "location": {
      "column": 8,
      "row": 1
    },
    "message": "` + "`os`" + ` imported but unused",
    "name": "unused-import",
    "noqa_row": 1,
    "severity": "error",
    "url": "https://docs.astral.sh/ruff/rules/unused-import"
  },
  {
    "cell": null,
    "code": "F841",
    "end_location": {
      "column": 11,
      "row": 5
    },
    "filename": "/workspace/dirty.py",
    "fix": {
      "applicability": "unsafe",
      "edits": [
        {
          "content": "",
          "end_location": {
            "column": 1,
            "row": 6
          },
          "location": {
            "column": 1,
            "row": 5
          }
        }
      ],
      "message": "Remove assignment to unused variable ` + "`unused`" + `"
    },
    "location": {
      "column": 5,
      "row": 5
    },
    "message": "Local variable ` + "`unused`" + ` is assigned to but never used",
    "name": "unused-variable",
    "noqa_row": 5,
    "severity": "error",
    "url": "https://docs.astral.sh/ruff/rules/unused-variable"
  }
]`

const syntaxErrorExitCode = 1

const syntaxErrorStdout = `[
  {
    "cell": null,
    "code": "invalid-syntax",
    "end_location": {
      "column": 11,
      "row": 1
    },
    "filename": "/workspace/solution.py",
    "fix": null,
    "location": {
      "column": 10,
      "row": 1
    },
    "message": "Expected a parameter or the end of the parameter list",
    "name": "invalid-syntax",
    "noqa_row": null,
    "severity": "error",
    "url": null
  },
  {
    "cell": null,
    "code": "invalid-syntax",
    "end_location": {
      "column": 1,
      "row": 2
    },
    "filename": "/workspace/solution.py",
    "fix": null,
    "location": {
      "column": 11,
      "row": 1
    },
    "message": "Expected ` + "`)`" + `, found newline",
    "name": "invalid-syntax",
    "noqa_row": null,
    "severity": "error",
    "url": null
  }
]`

const usageErrorExitCode = 2

const usageErrorStderr = `error: unexpected argument '--no-such-flag' found

  tip: a similar argument exists: '--no-show-fixes'

Usage: ruff check --no-cache --output-format <OUTPUT_FORMAT> [FILES]...

For more information, try '--help'.
`
