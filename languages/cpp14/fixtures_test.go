package cpp14

// Inline fixtures: real native clang-tidy output captured by running the tool in
// the image (workdir /workspace); the *ExitCode consts are the recorded
// exit statuses of those runs.

const cleanExitCode = 0

const dirtyExitCode = 0

const dirtyStdout = `/workspace/solution.cpp:5:8: warning: Dereference of null pointer (loaded from variable 'p') [clang-analyzer-core.NullDereference]
    5 |     *p = 42; // null dereference
      |      ~ ^
/workspace/solution.cpp:4:5: note: 'p' initialized to a null pointer value
    4 |     int *p = nullptr;
      |     ^~~~~~
/workspace/solution.cpp:5:8: note: Dereference of null pointer (loaded from variable 'p')
    5 |     *p = 42; // null dereference
      |      ~ ^
`

const dirtyStderr = `1 warning generated.
`
