package transport

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os/exec"
)

func runLocal(ctx context.Context, cmd string, stdin io.Reader) (Result, error) {
	c := exec.CommandContext(ctx, "sh", "-c", cmd)
	var out, errb bytes.Buffer
	c.Stdout, c.Stderr, c.Stdin = &out, &errb, stdin
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
		return res, nil
	}
	return res, err
}
