// Package wificaps reads what each Wi-Fi radio can really do (bands, channel
// widths, allowed channels) from "iw phy <name> info", so forms only offer
// choices the hardware and the regulatory domain allow.
package wificaps

import (
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Script prints, for every radio, a "##phy <name> <device path>" line followed
// by "iw phy <name> info", then the regulatory domain. It only reads.
const Script = `echo '##owrt-wifi-caps'
for p in /sys/class/ieee80211/*; do
  [ -e "$p" ] || continue
  n=${p##*/}
  echo "##phy $n $(readlink -f "$p/device" 2>/dev/null)"
  iw phy "$n" info 2>/dev/null
done
echo '##reg'
iw reg get 2>/dev/null
`

// Channel is one frequency a radio lists.
type Channel struct {
	Num      int
	MHz      int
	Disabled bool // not usable in the current regulatory domain
	NoIR     bool // may not start an access point on it (passive only)
	Radar    bool // DFS: needs radar detection, may be slow to start
}

// Usable reports whether an access point can start on the channel now. DFS
// channels are marked "no IR" until the radio has listened for radar, but an
// access point may use them, so radar channels count.
func (c Channel) Usable() bool { return !c.Disabled && (!c.NoIR || c.Radar) }

// NeedsCountry reports a channel that is only passive-scan under the current
// regulatory domain and may open up once a country is set.
func (c Channel) NeedsCountry() bool { return !c.Disabled && c.NoIR && !c.Radar }

// Band is one frequency band of a radio.
type Band struct {
	Name     string // "2g", "5g" or "6g"
	HT40     bool
	VHT      bool
	VHT160   bool
	HE       bool
	Channels []Channel
}

// Phy is one radio.
type Phy struct {
	Name  string // phy0
	Path  string // device path, to match a "path" option in /etc/config/wireless
	Bands []Band
}

// Caps is everything collected from the router.
type Caps struct {
	Phys []Phy
	// Country is the regulatory domain in force ("00" is the unset world domain).
	Country string
}

var (
	freqRe = regexp.MustCompile(`^\*\s+([0-9.]+) MHz \[(\d+)\](.*)$`)
	phyRe  = regexp.MustCompile(`^##phy (\S+)\s*(.*)$`)
	ctryRe = regexp.MustCompile(`^country (\S\S):`)
)

// Parse reads the output of Script.
func Parse(out string) Caps {
	var c Caps
	var cur *Phy
	var band *Band
	inReg := false
	flush := func() {
		if band != nil && cur != nil && len(band.Channels) > 0 {
			cur.Bands = append(cur.Bands, *band)
		}
		band = nil
	}
	for _, raw := range strings.Split(out, "\n") {
		line := strings.TrimRight(raw, "\r")
		t := strings.TrimSpace(line)
		if m := phyRe.FindStringSubmatch(line); m != nil {
			flush()
			if cur != nil {
				c.Phys = append(c.Phys, *cur)
			}
			cur, inReg = &Phy{Name: m[1], Path: strings.TrimSpace(m[2])}, false
			continue
		}
		if t == "##reg" {
			flush()
			if cur != nil {
				c.Phys = append(c.Phys, *cur)
				cur = nil
			}
			inReg = true
			continue
		}
		if inReg {
			if m := ctryRe.FindStringSubmatch(t); m != nil && c.Country == "" {
				c.Country = m[1]
			}
			continue
		}
		if cur == nil {
			continue
		}
		switch {
		case strings.HasPrefix(t, "Band ") && strings.HasSuffix(t, ":"):
			flush()
			band = &Band{}
		case band == nil:
		case strings.Contains(t, "HT20/HT40"):
			band.HT40 = true
		case strings.HasPrefix(t, "VHT Capabilities"):
			band.VHT = true
		case strings.HasPrefix(t, "Supported Channel Width:"):
			band.VHT160 = strings.Contains(t, "160") && !strings.Contains(t, "neither 160")
		case strings.HasPrefix(t, "HE Iftypes") || strings.HasPrefix(t, "HE MAC Capabilities"):
			band.HE = true
		default:
			if m := freqRe.FindStringSubmatch(t); m != nil {
				mhz, _ := strconv.ParseFloat(m[1], 64)
				n, _ := strconv.Atoi(m[2])
				ch := Channel{Num: n, MHz: int(mhz)}
				ch.Disabled = strings.Contains(m[3], "disabled")
				ch.NoIR = strings.Contains(m[3], "no IR")
				ch.Radar = strings.Contains(m[3], "radar")
				band.Channels = append(band.Channels, ch)
				band.Name = bandOf(ch.MHz)
			}
		}
	}
	flush()
	if cur != nil {
		c.Phys = append(c.Phys, *cur)
	}
	return c
}

func bandOf(mhz int) string {
	switch {
	case mhz < 3000:
		return "2g"
	case mhz < 5925:
		return "5g"
	}
	return "6g"
}

// For finds the radio that belongs to a wifi-device section: by its "path"
// option when it has one, otherwise radioN is phyN.
func (c Caps) For(radio, path string) (Phy, bool) {
	if path != "" {
		for _, p := range c.Phys {
			if p.Path != "" && strings.HasSuffix(p.Path, path) {
				return p, true
			}
		}
	}
	if n := strings.TrimPrefix(radio, "radio"); n != radio {
		for _, p := range c.Phys {
			if p.Name == "phy"+n {
				return p, true
			}
		}
	}
	return Phy{}, false
}

// Band returns the radio's band by name.
func (p Phy) Band(name string) (Band, bool) {
	for _, b := range p.Bands {
		if b.Name == name {
			return b, true
		}
	}
	return Band{}, false
}

// Allowed lists the channel numbers an access point can start on.
func (b Band) Allowed() []int {
	var out []int
	for _, c := range b.Channels {
		if c.Usable() {
			out = append(out, c.Num)
		}
	}
	sort.Ints(out)
	return out
}

// NeedCountry lists channels that a country code could unlock.
func (b Band) NeedCountry() []int {
	var out []int
	for _, c := range b.Channels {
		if c.NeedsCountry() {
			out = append(out, c.Num)
		}
	}
	sort.Ints(out)
	return out
}

// Channel looks one up.
func (b Band) Channel(n int) (Channel, bool) {
	for _, c := range b.Channels {
		if c.Num == n {
			return c, true
		}
	}
	return Channel{}, false
}

// HTModes lists the width settings the band supports, narrowest first.
func (b Band) HTModes() []string {
	var out []string
	add := func(prefix string, widths ...int) {
		for _, w := range widths {
			out = append(out, prefix+strconv.Itoa(w))
		}
	}
	switch {
	case b.Name == "2g" || !(b.VHT || b.HE):
		out = append(out, "HT20")
		if b.HT40 {
			out = append(out, "HT40")
		}
	default:
		if b.VHT {
			add("VHT", 20, 40, 80)
			if b.VHT160 {
				add("VHT", 160)
			}
		}
		if b.HE {
			add("HE", 20, 40, 80)
			if b.VHT160 {
				add("HE", 160)
			}
		}
	}
	if b.Name == "2g" && b.HE {
		out = append(out, "HE20", "HE40")
	}
	return out
}
