// Package validate checks values before they are staged for a router. The
// checks are strict on purpose: a value that passes is safe to put in a UCI
// option, and most wrong values are caught while the user is still typing.
package validate

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Func validates one field value. An empty string is handled by the caller
// (a field is either required or may be left empty), so a Func may assume s != "".
type Func func(s string) error

var errCtl = errors.New("may not contain control characters or line breaks")

func noCtl(s string) error {
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return errCtl
		}
	}
	return nil
}

// IPv4 accepts a dotted-quad address.
func IPv4(s string) error {
	a, err := netip.ParseAddr(s)
	if err != nil || !a.Is4() {
		return errors.New("not an IPv4 address (example: 192.168.1.1)")
	}
	return nil
}

// IP accepts either address family.
func IP(s string) error {
	if _, err := netip.ParseAddr(s); err != nil {
		return errors.New("not an IP address")
	}
	return nil
}

// Netmask accepts a dotted netmask such as 255.255.255.0 (contiguous ones).
func Netmask(s string) error {
	ip := net.ParseIP(s)
	if ip == nil || ip.To4() == nil {
		return errors.New("not a netmask (example: 255.255.255.0)")
	}
	m := net.IPMask(ip.To4())
	ones, bits := m.Size()
	if bits == 0 || ones == 0 {
		return errors.New("netmask must be contiguous (example: 255.255.255.0)")
	}
	return nil
}

// CIDR4 accepts 192.168.1.1/24 as well as a bare address.
func CIDR4(s string) error {
	if !strings.Contains(s, "/") {
		return IPv4(s)
	}
	p, err := netip.ParsePrefix(s)
	if err != nil || !p.Addr().Is4() {
		return errors.New("not an IPv4 address with prefix (example: 192.168.1.1/24)")
	}
	return nil
}

var macRe = regexp.MustCompile(`^([0-9A-Fa-f]{2}:){5}[0-9A-Fa-f]{2}$`)

// MAC accepts aa:bb:cc:dd:ee:ff.
func MAC(s string) error {
	if !macRe.MatchString(s) {
		return errors.New("not a MAC address (example: aa:bb:cc:dd:ee:ff)")
	}
	return nil
}

var labelRe = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?$`)

// Hostname accepts a DNS label or a dotted name.
func Hostname(s string) error {
	if len(s) > 253 {
		return errors.New("too long")
	}
	for _, l := range strings.Split(s, ".") {
		if !labelRe.MatchString(l) {
			return errors.New("letters, digits and '-' only (no spaces or underscores)")
		}
	}
	return nil
}

// Port accepts 1-65535.
func Port(s string) error { return IntRange(1, 65535)(s) }

// IntRange returns a validator for whole numbers in [lo, hi].
func IntRange(lo, hi int) Func {
	return func(s string) error {
		n, err := strconv.Atoi(s)
		if err != nil {
			return errors.New("not a whole number")
		}
		if n < lo || n > hi {
			return fmt.Errorf("must be between %d and %d", lo, hi)
		}
		return nil
	}
}

var durRe = regexp.MustCompile(`^(\d{1,9})([smhdw]?)$`)

// Duration accepts dnsmasq style lease times: 12h, 30m, 1d, 3600, or "infinite".
func Duration(s string) error {
	if s == "infinite" {
		return nil
	}
	if !durRe.MatchString(s) {
		return errors.New("use a number with s, m, h, d or w (example: 12h), or 'infinite'")
	}
	return nil
}

// SSID accepts 1-32 bytes of printable text.
func SSID(s string) error {
	if err := noCtl(s); err != nil {
		return err
	}
	if len(s) < 1 || len(s) > 32 {
		return errors.New("must be 1 to 32 bytes")
	}
	return nil
}

var hex64 = regexp.MustCompile(`^[0-9A-Fa-f]{64}$`)

// WPAKey accepts a WPA passphrase (8-63 printable characters) or a 64-digit hex key.
func WPAKey(s string) error {
	if err := noCtl(s); err != nil {
		return err
	}
	if hex64.MatchString(s) {
		return nil
	}
	n := utf8.RuneCountInString(s)
	if n < 8 || n > 63 {
		return errors.New("a passphrase needs 8 to 63 characters")
	}
	return nil
}

// Text accepts any text without control characters (length-limited).
func Text(max int) Func {
	return func(s string) error {
		if err := noCtl(s); err != nil {
			return err
		}
		if utf8.RuneCountInString(s) > max {
			return fmt.Errorf("at most %d characters", max)
		}
		return nil
	}
}

var uciIDRe = regexp.MustCompile(`^[A-Za-z0-9_]+$`)

// UCIName accepts what UCI allows for a section name.
func UCIName(s string) error {
	if len(s) > 63 || !uciIDRe.MatchString(s) {
		return errors.New("letters, digits and '_' only")
	}
	return nil
}

// Channel accepts a Wi-Fi channel number, or "auto".
func Channel(s string) error {
	if s == "auto" {
		return nil
	}
	return IntRange(1, 233)(s)
}

// OneOf returns a validator for a fixed set of values.
func OneOf(vals ...string) Func {
	return func(s string) error {
		for _, v := range vals {
			if s == v {
				return nil
			}
		}
		return fmt.Errorf("one of: %s", strings.Join(vals, ", "))
	}
}

// InSubnet reports whether ip is inside the prefix given as address+netmask or CIDR.
func InSubnet(ip, addr, mask string) bool {
	a, err := netip.ParseAddr(ip)
	if err != nil {
		return false
	}
	var p netip.Prefix
	if strings.Contains(addr, "/") {
		p, err = netip.ParsePrefix(addr)
	} else if m := net.ParseIP(mask); m != nil && m.To4() != nil {
		ones, _ := net.IPMask(m.To4()).Size()
		p, err = netip.ParsePrefix(fmt.Sprintf("%s/%d", addr, ones))
	} else {
		return false
	}
	return err == nil && p.Masked().Contains(a)
}
