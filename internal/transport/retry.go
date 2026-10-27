package transport

import (
	"bytes"
	"context"
	"errors"
	"io"
	"time"
)

// Retry retries a command once or more when the connection itself fails
// (ErrConn). Only wrap a transport in Retry when every command is a read: a
// dropped connection says nothing about whether the command already ran, so
// repeating a write could apply it twice. The read-only CLI qualifies; a
// session that can write must not use it.
type Retry struct {
	T        Transport
	Attempts int           // extra tries after the first; default 1
	Delay    time.Duration // pause between tries; default 700ms
}

func (r Retry) Describe() string { return r.T.Describe() }
func (r Retry) Close() error     { return r.T.Close() }

// Run implements Transport.
func (r Retry) Run(ctx context.Context, cmd string, stdin io.Reader) (Result, error) {
	attempts, delay := r.Attempts, r.Delay
	if attempts <= 0 {
		attempts = 1
	}
	if delay <= 0 {
		delay = 700 * time.Millisecond
	}
	// stdin is consumed by the first attempt, so keep a copy to replay.
	var body []byte
	if stdin != nil {
		var err error
		if body, err = io.ReadAll(stdin); err != nil {
			return Result{}, err
		}
	}
	for i := 0; ; i++ {
		var in io.Reader
		if stdin != nil {
			in = bytes.NewReader(body)
		}
		res, err := r.T.Run(ctx, cmd, in)
		if err == nil || !errors.Is(err, ErrConn) || i >= attempts {
			return res, err
		}
		select {
		case <-ctx.Done():
			return res, ctx.Err()
		case <-time.After(delay):
		}
	}
}

// Stream passes through without retrying: lines may already have been shown.
func (r Retry) Stream(ctx context.Context, cmd string, onLine func(string)) error {
	return Stream(ctx, r.T, cmd, onLine)
}
