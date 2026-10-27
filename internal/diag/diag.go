// Package diag runs network diagnostics from the router: ping, traceroute and
// nslookup. Hosts are validated before they reach a remote shell.
package diag

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/madushakv/owrt-tui/internal/shq"
	"github.com/madushakv/owrt-tui/internal/transport"
)

// PingResult is a parsed ping summary.
type PingResult struct {
	Host           string
	Sent, Received int
	LossPct        int
	Min, Avg, Max  float64 // milliseconds; zero when nothing was received
	Raw            string
}

var (
	pingStats = regexp.MustCompile(`(\d+) packets transmitted, (\d+) (?:packets )?received, (\d+)% packet loss`)
	pingRTT   = regexp.MustCompile(`(?:round-trip|rtt) min/avg/max(?:/mdev)? = ([\d.]+)/([\d.]+)/([\d.]+)`)
)

// PingCommand builds a ping command. count is clamped to 1..20.
func PingCommand(host string, count int) (string, error) {
	if !shq.Host(host) {
		return "", fmt.Errorf("invalid host %q", host)
	}
	if count < 1 {
		count = 1
	}
	if count > 20 {
		count = 20
	}
	return fmt.Sprintf("ping -c %d -W 2 %s", count, shq.Quote(host)), nil
}

// ParsePing parses ping output. It returns an error when no summary is present
// (for example an unresolvable host).
func ParsePing(host, out string) (PingResult, error) {
	r := PingResult{Host: host, Raw: out}
	m := pingStats.FindStringSubmatch(out)
	if m == nil {
		return r, fmt.Errorf("%s", firstLine(out, "no ping summary"))
	}
	r.Sent, _ = strconv.Atoi(m[1])
	r.Received, _ = strconv.Atoi(m[2])
	r.LossPct, _ = strconv.Atoi(m[3])
	if t := pingRTT.FindStringSubmatch(out); t != nil {
		r.Min, _ = strconv.ParseFloat(t[1], 64)
		r.Avg, _ = strconv.ParseFloat(t[2], 64)
		r.Max, _ = strconv.ParseFloat(t[3], 64)
	}
	return r, nil
}

// Ping pings host from the router. Total loss is a result, not an error.
func Ping(ctx context.Context, t transport.Transport, host string, count int) (PingResult, error) {
	cmd, err := PingCommand(host, count)
	if err != nil {
		return PingResult{}, err
	}
	res, err := t.Run(ctx, cmd+" 2>&1", nil)
	if err != nil {
		return PingResult{}, err
	}
	return ParsePing(host, res.Stdout)
}

// TracerouteCommand builds a numeric traceroute with a short per-hop wait.
func TracerouteCommand(host string) (string, error) {
	if !shq.Host(host) {
		return "", fmt.Errorf("invalid host %q", host)
	}
	return "traceroute -n -m 20 -w 2 " + shq.Quote(host), nil
}

// Traceroute streams hops through onLine as they are discovered.
func Traceroute(ctx context.Context, t transport.Transport, host string, onLine func(string)) error {
	cmd, err := TracerouteCommand(host)
	if err != nil {
		return err
	}
	return transport.Stream(ctx, t, cmd, onLine)
}

// NslookupCommand builds a lookup, optionally against a specific DNS server.
func NslookupCommand(name, server string) (string, error) {
	if !shq.Host(name) {
		return "", fmt.Errorf("invalid name %q", name)
	}
	cmd := "nslookup " + shq.Quote(name)
	if server != "" {
		if !shq.Host(server) {
			return "", fmt.Errorf("invalid DNS server %q", server)
		}
		cmd += " " + shq.Quote(server)
	}
	return cmd, nil
}

// Nslookup returns the raw lookup output. A failed lookup returns the output
// together with an error so the UI can show what the resolver said.
func Nslookup(ctx context.Context, t transport.Transport, name, server string) (string, error) {
	cmd, err := NslookupCommand(name, server)
	if err != nil {
		return "", err
	}
	res, err := t.Run(ctx, cmd+" 2>&1", nil)
	if err != nil {
		return "", err
	}
	if res.ExitCode != 0 {
		return res.Stdout, fmt.Errorf("lookup failed (exit %d)", res.ExitCode)
	}
	return res.Stdout, nil
}

func firstLine(s, def string) string {
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			return l
		}
	}
	return def
}
