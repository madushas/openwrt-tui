package transport

import (
	"context"
	"io"
	"strings"
	"sync"
)

// Local runs commands in a local shell. Handy for developing the parsers
// against canned scripts, never used against a real router.
type Local struct{}

func (Local) Describe() string { return "local" }
func (Local) Close() error     { return nil }
func (Local) Run(ctx context.Context, cmd string, stdin io.Reader) (Result, error) {
	return runLocal(ctx, cmd, stdin)
}

// Fake is a scripted transport for tests and for running the UI without a
// router. Handlers are checked in order; the first whose Match substring is
// found in the command (or, for "sh -s", in the script text) wins.
type Fake struct {
	mu       sync.Mutex
	handlers []fakeHandler
	Calls    []string
	Name     string
	Fail     error // when set, every Run returns this error
}

type fakeHandler struct {
	match string
	res   Result
	fn    func(cmd, stdin string) Result
}

// On registers a canned stdout for any command or script containing match.
func (f *Fake) On(match, stdout string) *Fake {
	f.handlers = append(f.handlers, fakeHandler{match: match, res: Result{Stdout: stdout}})
	return f
}

// OnResult registers a full canned result (exit code, stderr).
func (f *Fake) OnResult(match string, r Result) *Fake {
	f.handlers = append(f.handlers, fakeHandler{match: match, res: r})
	return f
}

// OnFunc registers a dynamic handler.
func (f *Fake) OnFunc(match string, fn func(cmd, stdin string) Result) *Fake {
	f.handlers = append(f.handlers, fakeHandler{match: match, fn: fn})
	return f
}

func (f *Fake) Describe() string {
	if f.Name != "" {
		return f.Name
	}
	return "fake"
}
func (f *Fake) Close() error { return nil }

// Run implements Transport.
func (f *Fake) Run(ctx context.Context, cmd string, stdin io.Reader) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	var in string
	if stdin != nil {
		b, _ := io.ReadAll(stdin)
		in = string(b)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Fail != nil {
		return Result{}, f.Fail
	}
	rec := cmd
	if in != "" {
		rec += "\n" + in
	}
	f.Calls = append(f.Calls, rec)
	hay := cmd + "\n" + in
	for _, h := range f.handlers {
		if strings.Contains(hay, h.match) {
			if h.fn != nil {
				return h.fn(cmd, in), nil
			}
			return h.res, nil
		}
	}
	return Result{Stderr: "fake: no handler for: " + cmd, ExitCode: 127}, nil
}

// CallsMatching returns recorded calls containing substr.
func (f *Fake) CallsMatching(substr string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, c := range f.Calls {
		if strings.Contains(c, substr) {
			out = append(out, c)
		}
	}
	return out
}
