package transport

import (
	"bufio"
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// Streamer is implemented by transports that can deliver output line by line
// while a long-running command (logread -f, traceroute) is still going.
type Streamer interface {
	Stream(ctx context.Context, cmd string, onLine func(string)) error
}

// Stream runs cmd and calls onLine for each output line. Transports without
// native streaming fall back to running the command to completion first.
func Stream(ctx context.Context, t Transport, cmd string, onLine func(string)) error {
	if s, ok := t.(Streamer); ok {
		return s.Stream(ctx, cmd, onLine)
	}
	res, err := t.Run(ctx, cmd+" 2>&1", nil)
	if err != nil {
		return err
	}
	for _, l := range strings.Split(strings.TrimRight(res.Stdout, "\n"), "\n") {
		if l != "" {
			onLine(l)
		}
	}
	return nil
}

// Stream implements Streamer. It asks for a pty (-tt) so that when the context
// is cancelled and ssh is killed, the remote process gets SIGHUP immediately
// instead of lingering until its next write fails. Dropbear without a pty
// would leave "logread -f" running until the next log line arrives.
func (s *SSH) Stream(ctx context.Context, cmd string, onLine func(string)) error {
	args := append(s.baseArgs(), "-tt", s.Target, cmd+" 2>&1")
	c := exec.CommandContext(ctx, s.binPath, args...)
	out, err := c.StdoutPipe()
	if err != nil {
		return err
	}
	if err := c.Start(); err != nil {
		return fmt.Errorf("%w: %v", ErrConn, err)
	}
	sc := bufio.NewScanner(out)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		onLine(Sanitize(strings.TrimRight(sc.Text(), "\r")))
	}
	err = c.Wait()
	if ctx.Err() != nil {
		return ctx.Err() // cancelled by the caller: expected way to stop a follow
	}
	if err != nil {
		return fmt.Errorf("%w: %v", ErrConn, err)
	}
	return nil
}
