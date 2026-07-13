package c

// Inline fixtures: real native clang-tidy output captured by running the tool in
// the image (workdir /workspace); the *ExitCode consts are the recorded
// exit statuses of those runs.

const cleanExitCode = 0

const compileErrorExitCode = 1

const compileErrorStdout = `/workspace/solution.c:2:12: error: use of undeclared identifier 'undeclared_identifier' [clang-diagnostic-error]
    2 |     return undeclared_identifier
      |            ^
/workspace/solution.c:2:33: error: expected ';' after return statement [clang-diagnostic-error]
    2 |     return undeclared_identifier
      |                                 ^
      |                                 ;
`

const compileErrorStderr = `2 errors generated.
Error while processing /workspace/solution.c.
Found compiler error(s).
`

const dirtyExitCode = 0

const dirtyStdout = `/workspace/solution.c:5:8: warning: Dereference of null pointer (loaded from variable 'p') [clang-analyzer-core.NullDereference]
    5 |     *p = 42; /* null dereference */
      |      ~ ^
/workspace/solution.c:4:5: note: 'p' initialized to a null pointer value
    4 |     int *p = 0;
      |     ^~~~~~
/workspace/solution.c:5:8: note: Dereference of null pointer (loaded from variable 'p')
    5 |     *p = 42; /* null dereference */
      |      ~ ^
`

const dirtyStderr = `1 warning generated.
`
