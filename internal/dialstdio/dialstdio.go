// Package dialstdio implements `cobe-lint dial-stdio`: it bridges stdin
// and stdout to the server's unix socket so that an exec of it gives the
// caller a byte stream to the server (docs/CONTRACT.md section 10).
package dialstdio

import (
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"runtime"
)

// DefaultSocket is serve's default unix socket.
const DefaultSocket = "/tmp/cobe-lint.sock"

// Exit codes of contract section 10.
const (
	exitOK      = 0
	exitConnect = 1
	exitUsage   = 2
	exitIO      = 3
)

// Main parses args (the flags after "dial-stdio") and bridges os.Stdin
// and os.Stdout.
func Main(args []string, stderr io.Writer) int {
	// Each open connection is one such process inside the container's pid
	// limit, so keep its threads few.
	runtime.GOMAXPROCS(1)
	fs := flag.NewFlagSet("cobe-lint dial-stdio", flag.ContinueOnError)
	fs.SetOutput(stderr)
	socket := fs.String("socket", DefaultSocket, "unix socket of cobe-lint serve")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	if fs.NArg() != 0 {
		fmt.Fprintf(stderr, "cobe-lint: dial-stdio: unexpected argument %q\n", fs.Arg(0))
		return exitUsage
	}
	return Run(*socket, os.Stdin, os.Stdout, stderr)
}

// Run connects to socket and copies stdin to it and it to stdout. It
// half-closes the socket on stdin EOF and returns as soon as the socket
// reaches EOF, without waiting for stdin.
func Run(socket string, stdin io.Reader, stdout io.Writer, stderr io.Writer) int {
	conn, err := net.Dial("unix", socket)
	if err != nil {
		fmt.Fprintf(stderr, "cobe-lint: dial-stdio: %v\n", err)
		return exitConnect
	}
	uc := conn.(*net.UnixConn)
	defer uc.Close()

	go func() {
		if _, err := io.Copy(uc, stdin); err == nil {
			_ = uc.CloseWrite()
		}
	}()

	_, err = io.Copy(stdout, uc)
	if err != nil {
		fmt.Fprintf(stderr, "cobe-lint: dial-stdio: %v\n", err)
		return exitIO
	}
	// The server closed the connection: exit without waiting for stdin.
	// A write that failed because of that close is not an error.
	return exitOK
}
