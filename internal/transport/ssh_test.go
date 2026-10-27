//go:build unix

package transport

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// A stand-in for the ssh binary. It records its arguments, answers "-O check"
// twice before reporting the control socket gone (so Close has to poll), fails
// once with exit 255 and no message when asked, and otherwise echoes stdin and
// the remote command.
const fakeSSH = `#!/bin/sh
echo "$*" >> "$FAKE_SSH_STATE/calls"
case " $* " in
  *" -O check "*)
    n=$(cat "$FAKE_SSH_STATE/check" 2>/dev/null || echo 0); n=$((n+1)); echo $n > "$FAKE_SSH_STATE/check"
    [ "$n" -le 2 ] && exit 0
    exit 255 ;;
  *" -O exit "*) exit 0 ;;
esac
if [ -e "$FAKE_SSH_STATE/fail_once" ]; then rm "$FAKE_SSH_STATE/fail_once"; exit 255; fi
if [ -e "$FAKE_SSH_STATE/stderr_255" ]; then echo "ssh: connect to host r port 22: Connection refused" >&2; exit 255; fi
cat
for a; do last=$a; done
echo "ran: $last"
`

func newFakeSSH(t *testing.T) (*SSH, string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "ssh"), []byte(fakeSSH), 0o755); err != nil {
		t.Fatal(err)
	}
	state := t.TempDir()
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("FAKE_SSH_STATE", state)
	s, err := NewSSH("root@router", true)
	if err != nil {
		t.Fatal(err)
	}
	return s, state
}

func touch(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
}

func commandCalls(t *testing.T, state string) int {
	t.Helper()
	b, _ := os.ReadFile(filepath.Join(state, "calls"))
	n := 0
	for _, l := range strings.Split(string(b), "\n") {
		if l != "" && !strings.Contains(l, " -O ") {
			n++
		}
	}
	return n
}

func TestSSHRunPassesCommandAndStdin(t *testing.T) {
	s, _ := newFakeSSH(t)
	res, err := s.Run(context.Background(), "echo hi", strings.NewReader("script-body\n"))
	if err != nil || !strings.Contains(res.Stdout, "script-body") || !strings.Contains(res.Stdout, "ran: echo hi") {
		t.Fatalf("%+v %v", res, err)
	}
}

func TestSilent255ExplainsItself(t *testing.T) {
	s, state := newFakeSSH(t)
	touch(t, filepath.Join(state, "fail_once"))
	_, err := s.Run(context.Background(), "true", nil)
	if !errors.Is(err, ErrConn) || !strings.Contains(err.Error(), "no message") {
		t.Fatalf("an empty 255 must say so, got %v", err)
	}
}

func TestStderr255IsReportedVerbatim(t *testing.T) {
	s, state := newFakeSSH(t)
	touch(t, filepath.Join(state, "stderr_255"))
	_, err := s.Run(context.Background(), "true", nil)
	if !errors.Is(err, ErrConn) || !strings.Contains(err.Error(), "Connection refused") {
		t.Fatalf("got %v", err)
	}
}

func TestCloseWaitsForMasterToExit(t *testing.T) {
	s, state := newFakeSSH(t)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(state, "check"))
	if got := strings.TrimSpace(string(b)); got != "3" {
		t.Fatalf("Close should keep checking until the socket is gone (2 alive + 1 gone = 3 checks), checked %q times", got)
	}
}

func TestRetryRecoversAndReplaysStdin(t *testing.T) {
	s, state := newFakeSSH(t)
	touch(t, filepath.Join(state, "fail_once"))
	r := Retry{T: s, Delay: time.Millisecond}
	res, err := r.Run(context.Background(), "sh -s", strings.NewReader("the-script\n"))
	if err != nil {
		t.Fatalf("one retry should hide a single transient failure: %v", err)
	}
	if !strings.Contains(res.Stdout, "the-script") {
		t.Fatalf("stdin must be replayed on the retry, got %q", res.Stdout)
	}
	if n := commandCalls(t, state); n != 2 {
		t.Fatalf("expected 2 attempts, got %d", n)
	}
}

func TestRetryGivesUpAfterConfiguredAttempts(t *testing.T) {
	s, state := newFakeSSH(t)
	touch(t, filepath.Join(state, "stderr_255")) // fails every time
	r := Retry{T: s, Attempts: 2, Delay: time.Millisecond}
	if _, err := r.Run(context.Background(), "true", nil); !errors.Is(err, ErrConn) {
		t.Fatalf("got %v", err)
	}
	if n := commandCalls(t, state); n != 3 {
		t.Fatalf("1 try + 2 retries = 3 attempts, got %d", n)
	}
}

type failingTransport struct {
	calls atomic.Int32
	err   error
}

func (f *failingTransport) Run(_ context.Context, _ string, _ io.Reader) (Result, error) {
	f.calls.Add(1)
	return Result{}, f.err
}
func (f *failingTransport) Describe() string { return "failing" }
func (f *failingTransport) Close() error     { return nil }

func TestRetryDoesNotRepeatOtherErrors(t *testing.T) {
	boom := errors.New("not a connection problem")
	f := &failingTransport{err: boom}
	r := Retry{T: f, Delay: time.Millisecond}
	if _, err := r.Run(context.Background(), "x", nil); !errors.Is(err, boom) {
		t.Fatalf("got %v", err)
	}
	if f.calls.Load() != 1 {
		t.Fatalf("only connection failures may be retried; ran %d times", f.calls.Load())
	}
}

func TestRetryStopsWhenContextEnds(t *testing.T) {
	f := &failingTransport{err: ErrConn}
	r := Retry{T: f, Attempts: 5, Delay: time.Hour}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := r.Run(ctx, "x", nil)
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > time.Second {
		t.Fatalf("must not sleep through cancellation: %v after %v", err, time.Since(start))
	}
}
