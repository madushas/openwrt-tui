package transport

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// SSH drives the system ssh binary with a ControlMaster so every command reuses
// one authenticated connection. That keeps the user's ~/.ssh/config, agent,
// ProxyJump and known_hosts behavior exactly as they already have it.
//
// Unix only (ControlMaster sockets). Windows support would use native SSH.
type SSH struct {
	Target  string   // host alias or user@host
	Args    []string // extra ssh args, e.g. -p 2222 -i key
	Batch   bool     // BatchMode=yes: never prompt (a TUI cannot show prompts)
	ctlDir  string
	binPath string
}

// NewSSH prepares an SSH transport. It does not connect yet.
func NewSSH(target string, batch bool, extra ...string) (*SSH, error) {
	bin, err := exec.LookPath("ssh")
	if err != nil {
		return nil, fmt.Errorf("ssh binary not found in PATH: %w", err)
	}
	dir := filepath.Join(os.TempDir(), fmt.Sprintf("owrt-tui-%d", os.Getuid()))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	return &SSH{Target: target, Args: extra, Batch: batch, ctlDir: dir, binPath: bin}, nil
}

func (s *SSH) baseArgs() []string {
	a := []string{
		"-o", "ControlMaster=auto",
		"-o", "ControlPath=" + filepath.Join(s.ctlDir, "%C"),
		"-o", "ControlPersist=120",
		"-o", "ServerAliveInterval=15",
		"-o", "ServerAliveCountMax=3",
		"-o", "ConnectTimeout=10",
	}
	if s.Batch {
		a = append(a, "-o", "BatchMode=yes")
	}
	return append(a, s.Args...)
}

func (s *SSH) Describe() string { return s.Target }

// Run implements Transport.
func (s *SSH) Run(ctx context.Context, cmd string, stdin io.Reader) (Result, error) {
	args := append(s.baseArgs(), s.Target, cmd)
	c := exec.CommandContext(ctx, s.binPath, args...)
	var out, errb bytes.Buffer
	c.Stdout, c.Stderr = &out, &errb
	c.Stdin = stdin
	err := c.Run()
	res := Result{Stdout: out.String(), Stderr: errb.String()}
	if err == nil {
		return res, nil
	}
	if ctx.Err() != nil {
		return res, ctx.Err()
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		res.ExitCode = ee.ExitCode()
		// ssh itself exits 255 on connection or auth failure.
		if res.ExitCode == 255 {
			return res, fmt.Errorf("%w: %s", ErrConn, describe255(res.Stderr))
		}
		return res, nil
	}
	return res, fmt.Errorf("%w: %v", ErrConn, err)
}

// Close tears down the shared master connection and waits until it is really
// gone. "ssh -O exit" returns as soon as the master acknowledges, but the master
// keeps shutting down for a moment afterwards. A command started in that window
// attaches to the dying master and fails with exit 255 and no message, which
// showed up as intermittent "connection failed:" errors when commands were run
// back to back.
func (s *SSH) Close() error {
	if err := exec.Command(s.binPath, append(s.baseArgs(), "-O", "exit", s.Target)...).Run(); err != nil {
		return nil // no master running, nothing to wait for
	}
	for i := 0; i < 30; i++ { // up to ~3s
		if exec.Command(s.binPath, append(s.baseArgs(), "-O", "check", s.Target)...).Run() != nil {
			return nil // check fails once the control socket is gone
		}
		time.Sleep(100 * time.Millisecond)
	}
	return nil
}

// describe255 turns the stderr of a failed ssh into one readable line. An empty
// stderr is itself information: ssh died without explaining, which usually
// means the connection dropped or a stale control master was reused.
func describe255(stderr string) string {
	var parts []string
	for _, l := range strings.Split(stderr, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			parts = append(parts, l)
		}
	}
	if len(parts) == 0 {
		return "ssh exited with status 255 and no message (connection dropped, or a stale shared connection was reused)"
	}
	msg := strings.Join(parts, "; ")
	if len(msg) > 300 {
		msg = msg[:300] + "..."
	}
	return msg
}
