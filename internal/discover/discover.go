// Package discover finds likely routers and the user's existing ssh material:
// the default gateway, Host entries in ~/.ssh/config, recently used targets and
// public keys. It only reads local files and local commands; it never contacts
// a host.
package discover

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
)

// ---- default gateway ----

// Gateway returns the IPv4 default gateway of this computer. It is only a
// guess at the router: with a VPN or double NAT it can be something else.
func Gateway() (string, error) {
	switch runtime.GOOS {
	case "linux":
		b, err := os.ReadFile("/proc/net/route")
		if err != nil {
			return "", err
		}
		return ParseProcRoute(string(b))
	case "darwin":
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		out, err := exec.CommandContext(ctx, "route", "-n", "get", "default").Output()
		if err != nil {
			return "", err
		}
		return ParseRouteGet(string(out))
	}
	return "", fmt.Errorf("default gateway lookup is not supported on %s", runtime.GOOS)
}

// ParseProcRoute reads /proc/net/route. Addresses are little-endian hex. When
// several default routes exist the one with the lowest metric wins.
func ParseProcRoute(text string) (string, error) {
	best, bestMetric := "", int(^uint(0)>>1)
	for i, line := range strings.Split(text, "\n") {
		f := strings.Fields(line)
		if i == 0 || len(f) < 8 || f[1] != "00000000" || f[7] != "00000000" {
			continue
		}
		flags, _ := strconv.ParseInt(f[3], 16, 32)
		if flags&0x1 == 0 || flags&0x2 == 0 { // need RTF_UP and RTF_GATEWAY
			continue
		}
		n, err := strconv.ParseUint(f[2], 16, 32)
		if err != nil || n == 0 {
			continue
		}
		metric, _ := strconv.Atoi(f[6])
		if metric < bestMetric {
			bestMetric = metric
			best = net.IPv4(byte(n), byte(n>>8), byte(n>>16), byte(n>>24)).String()
		}
	}
	if best == "" {
		return "", errors.New("no default gateway found")
	}
	return best, nil
}

// ParseRouteGet reads "route -n get default" output from macOS.
func ParseRouteGet(text string) (string, error) {
	for _, line := range strings.Split(text, "\n") {
		if k, v, ok := strings.Cut(strings.TrimSpace(line), ":"); ok && k == "gateway" {
			if ip := net.ParseIP(strings.TrimSpace(v)); ip != nil && ip.To4() != nil {
				return ip.String(), nil
			}
		}
	}
	return "", errors.New("no default gateway found")
}

// ---- targets ----

var targetRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.-]{0,31}@[A-Za-z0-9][A-Za-z0-9._:-]{0,252}$`)
var aliasRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// NormalizeTarget turns user input into an ssh target. A bare host gets
// "root@" in front. It rejects anything that could be read as an ssh option
// (a leading "-") or that carries spaces or shell characters.
func NormalizeTarget(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", errors.New("empty address")
	}
	if strings.HasPrefix(s, "-") {
		return "", errors.New("an address cannot start with '-'")
	}
	if !strings.Contains(s, "@") {
		if !aliasRe.MatchString(s) && net.ParseIP(s) == nil {
			return "", fmt.Errorf("%q is not a valid host name or address", s)
		}
		return "root@" + s, nil
	}
	if !targetRe.MatchString(s) {
		return "", fmt.Errorf("%q is not a valid user@host", s)
	}
	return s, nil
}

// HostOf returns the host part of user@host.
func HostOf(target string) string {
	if _, h, ok := strings.Cut(target, "@"); ok {
		return h
	}
	return target
}

// ---- ssh config ----

// SSHConfigHosts returns the concrete Host names in an ssh config file
// (wildcards and negations are skipped). Include files are not followed.
func SSHConfigHosts(path string) []string {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	return ParseHosts(string(b))
}

// ParseHosts extracts concrete Host aliases from ssh config text.
func ParseHosts(text string) []string {
	var out []string
	seen := map[string]bool{}
	sc := bufio.NewScanner(strings.NewReader(text))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 2 || !strings.EqualFold(f[0], "host") {
			continue
		}
		for _, h := range f[1:] {
			if strings.HasPrefix(h, "#") {
				break
			}
			if strings.ContainsAny(h, "*?!") || !aliasRe.MatchString(h) || seen[h] {
				continue
			}
			seen[h] = true
			out = append(out, h)
		}
	}
	return out
}

// HostEntry is a block to add to ~/.ssh/config.
type HostEntry struct {
	Alias, HostName, User, Port, IdentityFile string
}

// Render returns the config text for the entry.
func (h HostEntry) Render() (string, error) {
	if !aliasRe.MatchString(h.Alias) {
		return "", fmt.Errorf("invalid alias %q", h.Alias)
	}
	if h.HostName == "" || strings.ContainsAny(h.HostName+h.User+h.Port+h.IdentityFile, " \t\r\n\x00") {
		return "", errors.New("host entry values may not be empty or contain whitespace")
	}
	var b strings.Builder
	fmt.Fprintf(&b, "\n# added by owrt-tui\nHost %s\n    HostName %s\n", h.Alias, h.HostName)
	if h.User != "" {
		fmt.Fprintf(&b, "    User %s\n", h.User)
	}
	if h.Port != "" {
		fmt.Fprintf(&b, "    Port %s\n", h.Port)
	}
	if h.IdentityFile != "" {
		fmt.Fprintf(&b, "    IdentityFile %s\n", h.IdentityFile)
	}
	return b.String(), nil
}

// AppendHost appends the entry to the ssh config file. It never rewrites
// existing content, and refuses when the alias already exists.
func AppendHost(path string, h HostEntry) error {
	text, err := h.Render()
	if err != nil {
		return err
	}
	for _, e := range SSHConfigHosts(path) {
		if e == h.Alias {
			return fmt.Errorf("%s already has a Host %s entry", path, h.Alias)
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.WriteString(text); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// SafeAlias derives a config alias from a router hostname.
func SafeAlias(hostname string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(hostname) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-':
			b.WriteRune(r)
		}
	}
	if b.Len() == 0 {
		return "openwrt"
	}
	return b.String()
}

// ---- recent targets ----

const maxRecent = 5

// Recent returns the last used targets, newest first.
func Recent(dir string) []string {
	b, err := os.ReadFile(filepath.Join(dir, "recent"))
	if err != nil {
		return nil
	}
	var out []string
	for _, l := range strings.Split(string(b), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			if _, err := NormalizeTarget(l); err == nil {
				out = append(out, l)
			}
		}
	}
	return out
}

// AddRecent records a target as most recently used.
func AddRecent(dir, target string) error {
	list := []string{target}
	for _, r := range Recent(dir) {
		if r != target && len(list) < maxRecent {
			list = append(list, r)
		}
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "recent"), []byte(strings.Join(list, "\n")+"\n"), 0o600)
}

// ConfigDir is where owrt-tui keeps its own small state.
func ConfigDir() string {
	d, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(d, "owrt-tui")
}

// ---- keys ----

// Key is a public key found in ~/.ssh.
type Key struct {
	Path    string // path of the .pub file
	Private string // path of the matching private key
	Type    string
	Comment string
	Line    string // the full public key line
}

// ValidPubKey reports whether line is a single, plain public key line that is
// safe to send to a router.
func ValidPubKey(line string) bool {
	if strings.ContainsAny(line, "\r\n\x00") || len(line) > 2000 {
		return false
	}
	f := strings.Fields(line)
	if len(f) < 2 {
		return false
	}
	ok := strings.HasPrefix(f[0], "ssh-") || strings.HasPrefix(f[0], "ecdsa-") || strings.HasPrefix(f[0], "sk-")
	return ok && regexp.MustCompile(`^[A-Za-z0-9+/=]+$`).MatchString(f[1])
}

// PubKeys lists the usable public keys in dir (normally ~/.ssh).
func PubKeys(dir string) []Key {
	files, _ := filepath.Glob(filepath.Join(dir, "*.pub"))
	sort.Strings(files)
	var out []Key
	for _, p := range files {
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		line := strings.TrimSpace(strings.SplitN(string(b), "\n", 2)[0])
		if !ValidPubKey(line) {
			continue
		}
		f := strings.Fields(line)
		k := Key{Path: p, Private: strings.TrimSuffix(p, ".pub"), Type: f[0], Line: line}
		if len(f) > 2 {
			k.Comment = strings.Join(f[2:], " ")
		}
		out = append(out, k)
	}
	return out
}

// SSHDir returns ~/.ssh.
func SSHDir() string {
	h, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(h, ".ssh")
}

// NewKeyPath picks a path for a new ed25519 key without overwriting anything.
func NewKeyPath(dir string) string {
	for _, name := range []string{"id_ed25519", "id_ed25519_owrt"} {
		p := filepath.Join(dir, name)
		if _, err := os.Stat(p); err != nil {
			if _, err2 := os.Stat(p + ".pub"); err2 != nil {
				return p
			}
		}
	}
	for i := 2; ; i++ {
		p := filepath.Join(dir, fmt.Sprintf("id_ed25519_owrt%d", i))
		if _, err := os.Stat(p); err != nil {
			if _, err2 := os.Stat(p + ".pub"); err2 != nil {
				return p
			}
		}
	}
}

// IsDefaultIdentity reports whether ssh tries this private key without -i.
func IsDefaultIdentity(private string) bool {
	switch filepath.Base(private) {
	case "id_rsa", "id_ecdsa", "id_ecdsa_sk", "id_ed25519", "id_ed25519_sk", "id_dsa":
		return true
	}
	return false
}

// ReadPubKey reads one public key file.
func ReadPubKey(path string) (Key, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Key{}, err
	}
	line := strings.TrimSpace(strings.SplitN(string(b), "\n", 2)[0])
	if !ValidPubKey(line) {
		return Key{}, fmt.Errorf("%s does not contain a valid public key", path)
	}
	f := strings.Fields(line)
	k := Key{Path: path, Private: strings.TrimSuffix(path, ".pub"), Type: f[0], Line: line}
	if len(f) > 2 {
		k.Comment = strings.Join(f[2:], " ")
	}
	return k, nil
}

// ExplicitTarget validates a target given on the command line. Unlike
// NormalizeTarget it keeps a bare name as it is, because it may be a Host alias
// whose user and options come from ~/.ssh/config.
func ExplicitTarget(s string) (string, error) {
	s = strings.TrimSpace(s)
	if strings.Contains(s, "@") {
		return NormalizeTarget(s)
	}
	if strings.HasPrefix(s, "-") || !(aliasRe.MatchString(s) || net.ParseIP(s) != nil) {
		return "", fmt.Errorf("%q is not a valid host name, address or ssh Host alias", s)
	}
	return s, nil
}
