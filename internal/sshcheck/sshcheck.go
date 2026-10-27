// Package sshcheck turns ssh failures into something a person can act on, and
// builds the interactive commands used to set up key-based login. It shells out
// to the system ssh tools, so the user's ssh configuration always applies.
package sshcheck

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/madushakv/owrt-tui/internal/discover"
)

// Kind is why a connection attempt failed.
type Kind int

const (
	Other Kind = iota
	HostKeyUnknown
	HostKeyChanged
	AuthDenied
	Refused
	Timeout
	Unreachable
)

func (k Kind) String() string {
	return [...]string{"other", "unknown host key", "host key changed", "authentication failed",
		"connection refused", "timed out", "unreachable"}[k]
}

// Classify maps ssh's error text to a Kind. The changed-key warning is checked
// before the unknown-key case because ssh prints both lines for a changed key.
func Classify(msg string) Kind {
	m := strings.ToLower(msg)
	has := func(subs ...string) bool {
		for _, s := range subs {
			if strings.Contains(m, s) {
				return true
			}
		}
		return false
	}
	switch {
	case has("remote host identification has changed", "host key for") && has("has changed"):
		return HostKeyChanged
	case has("host key verification failed"):
		return HostKeyUnknown
	case has("permission denied", "too many authentication failures", "no supported authentication methods"):
		return AuthDenied
	case has("connection refused"):
		return Refused
	case has("timed out", "timeout"):
		return Timeout
	case has("no route to host", "network is unreachable", "could not resolve hostname",
		"name or service not known", "nodename nor servname", "temporary failure in name resolution"):
		return Unreachable
	}
	return Other
}

var offending = regexp.MustCompile(`(?i)offending \w+ key in ([^\s:]+):(\d+)`)

// OffendingKey returns "file:line" of the known_hosts entry that no longer
// matches, when ssh reported one.
func OffendingKey(msg string) string {
	if m := offending.FindStringSubmatch(msg); m != nil {
		return m[1] + ":" + m[2]
	}
	return ""
}

// Hint is a short explanation and what to try next.
func Hint(k Kind, target string) string {
	switch k {
	case HostKeyUnknown:
		return "This computer has not seen " + target + " before."
	case HostKeyChanged:
		return "The host key of " + target + " is different from the one saved earlier. That happens after a reinstall or factory reset, but it is also what a man-in-the-middle looks like. owrt-tui will not connect until you have checked it yourself and fixed known_hosts."
	case AuthDenied:
		return target + " is reachable but did not accept any of your keys (or has no SSH key set up for this account)."
	case Refused:
		return "Nothing is accepting SSH on " + target + ". The SSH server (dropbear) may be off, listening on another port, or this is not the router. Try -p with another port, or another address."
	case Timeout:
		return "No answer from " + target + ". The address may be wrong, or the router is on another network or VLAN you cannot reach."
	case Unreachable:
		return "Cannot reach " + target + ": the name does not resolve or there is no route to it from this network."
	}
	return "Could not connect to " + target + "."
}

// ---- host keys ----

// HostKey is one scanned host key, with the places it would be stored.
type HostKey struct {
	Line        string // known_hosts line as printed by ssh-keyscan
	Type        string // ssh-ed25519, ecdsa-sha2-nistp256, ssh-rsa, ...
	Fingerprint string // SHA256:...
	Host, Port  string
	KnownHosts  string // file the key will be appended to
}

var keyPref = []string{"ssh-ed25519", "ecdsa-sha2-nistp256", "ecdsa-sha2-nistp384", "ecdsa-sha2-nistp521", "ssh-rsa"}

// ParseKeyscan picks the strongest key from ssh-keyscan output.
func ParseKeyscan(out string) (line, typ string, err error) {
	best := len(keyPref) + 1
	for _, l := range strings.Split(out, "\n") {
		l = strings.TrimSpace(l)
		f := strings.Fields(l)
		if l == "" || strings.HasPrefix(l, "#") || len(f) != 3 {
			continue
		}
		for i, p := range keyPref {
			if f[1] == p && i < best {
				best, line, typ = i, l, p
			}
		}
	}
	if line == "" {
		return "", "", errors.New("the host did not offer a usable SSH host key")
	}
	return line, typ, nil
}

// ParseFingerprint reads "ssh-keygen -lf -" output: "256 SHA256:xxx comment (ED25519)".
func ParseFingerprint(out string) string {
	for _, f := range strings.Fields(out) {
		if strings.HasPrefix(f, "SHA256:") || strings.HasPrefix(f, "MD5:") {
			return f
		}
	}
	return ""
}

// ParseG reads "ssh -G" output and returns the effective host, port and first
// known_hosts file.
func ParseG(out string) (host, port, known string) {
	for _, l := range strings.Split(out, "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(l), " ")
		if !ok {
			continue
		}
		switch k {
		case "hostname":
			host = v
		case "port":
			port = v
		case "userknownhostsfile":
			if known == "" {
				known = strings.Fields(v)[0]
			}
		}
	}
	return
}

func expandHome(p string) string {
	if strings.HasPrefix(p, "~/") {
		if h, err := os.UserHomeDir(); err == nil {
			return filepath.Join(h, p[2:])
		}
	}
	return p
}

func output(ctx context.Context, stdin string, name string, args ...string) (string, error) {
	var out, errb bytes.Buffer
	c := exec.CommandContext(ctx, name, args...)
	c.Stdout, c.Stderr = &out, &errb
	if stdin != "" {
		c.Stdin = strings.NewReader(stdin)
	}
	if err := c.Run(); err != nil {
		if s := strings.TrimSpace(errb.String()); s != "" {
			return out.String(), fmt.Errorf("%s: %s", name, s)
		}
		return out.String(), fmt.Errorf("%s: %w", name, err)
	}
	return out.String(), nil
}

// Scan fetches the host key of target without trusting it. extra are the ssh
// options already in use (for example -p and -i); only -p matters here.
func Scan(ctx context.Context, target string, extra []string) (HostKey, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	// Ask ssh itself what "target" means, so Host aliases in ~/.ssh/config work.
	gargs := append(append([]string{"-G"}, extra...), target)
	gout, err := output(ctx, "", "ssh", gargs...)
	if err != nil {
		return HostKey{}, err
	}
	host, port, known := ParseG(gout)
	if host == "" {
		host = discover.HostOf(target)
	}
	if port == "" {
		port = "22"
	}
	if known == "" {
		known = "~/.ssh/known_hosts"
	}
	sargs := []string{"-T", "5", "-p", port, host}
	sout, err := output(ctx, "", "ssh-keyscan", sargs...)
	if err != nil && strings.TrimSpace(sout) == "" {
		return HostKey{}, err
	}
	line, typ, err := ParseKeyscan(sout)
	if err != nil {
		return HostKey{}, err
	}
	fout, err := output(ctx, line+"\n", "ssh-keygen", "-lf", "-")
	if err != nil {
		return HostKey{}, err
	}
	fp := ParseFingerprint(fout)
	if fp == "" {
		return HostKey{}, errors.New("could not compute the host key fingerprint")
	}
	return HostKey{Line: line, Type: typ, Fingerprint: fp, Host: host, Port: port, KnownHosts: expandHome(known)}, nil
}

// Pin stores exactly the key whose fingerprint the user saw. Storing the
// scanned key (instead of letting ssh accept whatever it meets next) means the
// connection that follows can only succeed with that same key.
func (h HostKey) Pin() error {
	if h.Line == "" || strings.ContainsAny(h.Line, "\r\n") || h.KnownHosts == "" {
		return errors.New("nothing to pin")
	}
	if err := os.MkdirAll(filepath.Dir(h.KnownHosts), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(h.KnownHosts, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.WriteString(h.Line + "\n"); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// ---- interactive commands (run with the terminal handed over) ----

// KeygenCmd builds the ssh-keygen command that creates a new ed25519 key. It
// asks for the passphrase itself, on the terminal.
func KeygenCmd(path string) *exec.Cmd {
	return exec.Command("ssh-keygen", "-t", "ed25519", "-f", path, "-C", "owrt-tui")
}

// installScript reads the key from stdin, so the key text is never part of the
// command line or interpolated into shell code.
const installScript = `set -e; k=$(cat); f=/etc/dropbear/authorized_keys; mkdir -p /etc/dropbear; touch "$f"; grep -qxF -- "$k" "$f" || printf '%s\n' "$k" >> "$f"; chmod 600 "$f"`

// InstallCmd builds the ssh command that appends a public key to the router's
// dropbear authorized_keys. ssh prompts for the password on the terminal, so
// this program never handles it. extra are ssh options such as -p.
func InstallCmd(target string, extra []string, pubLine string) (*exec.Cmd, error) {
	if !discover.ValidPubKey(pubLine) {
		return nil, errors.New("not a valid public key line")
	}
	if _, err := discover.NormalizeTarget(target); err != nil {
		return nil, err
	}
	args := append([]string{"-o", "ConnectTimeout=10"}, extra...)
	args = append(args, "--", target, installScript)
	c := exec.Command("ssh", args...)
	c.Stdin = strings.NewReader(strings.TrimSpace(pubLine) + "\n")
	return c, nil
}
