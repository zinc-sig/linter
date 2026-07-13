package golang

// Inline fixtures: real native `go vet` output captured by running the tool in
// the image (workdir /workspace); the *ExitCode consts are the recorded
// exit statuses of those runs.

const cleanExitCode = 0

const compileErrorExitCode = 1

const compileErrorStderr = `# command-line-arguments
# [command-line-arguments]
vet: ./solution.go:4:7: undefined: foo
`

const dirtyExitCode = 1

const dirtyStderr = `# command-line-arguments
# [command-line-arguments]
./solution.go:7:2: fmt.Printf format %d has arg name of wrong type string
`
