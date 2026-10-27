// Package transport runs commands on the router. The rest of the program only
// sees the Transport interface, so SSH, a local shell (for development) and a
// scripted fake (for tests) are interchangeable.
package transport

import (
	"context"
	"errors"
	"io"
	"strings"
)

// Result is the outcome of a remote command that actually ran.
type Result struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

// OK reports whether the command exited 0.
func (r Result) OK() bool { return r.ExitCode == 0 }

// ErrConn means the transport itself failed (no route, auth failure, ssh died),
// as opposed to the remote command exiting non-zero.
var ErrConn = errors.New("connection failed")

// Transport executes commands on the device.
type Transport interface {
	// Run executes cmd through the remote shell. A non-zero exit is reported in
	// Result.ExitCode with a nil error; only transport failures return an error.
	Run(ctx context.Context, cmd string, stdin io.Reader) (Result, error)
	// Describe returns a short label such as "root@192.168.1.1".
	Describe() string
	Close() error
}

// RunScript feeds script to "sh -s" on the device. This avoids all command-line
// quoting problems for multi-line work.
func RunScript(ctx context.Context, t Transport, script string) (Result, error) {
	return t.Run(ctx, "sh -s", strings.NewReader(script))
}
