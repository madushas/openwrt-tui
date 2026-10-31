package health

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Handshake is the last WireGuard handshake with one peer; At is a Unix time,
// 0 when the peer has never completed one.
type Handshake struct {
	Iface, Peer string
	At          int64
}

// LTE is the cellular signal quality. Zero fields were not reported.
type LTE struct {
	RSRP, RSRQ, RSSI int
	SNR              float64
	HasRSRP, HasSNR  bool
	Tech, Operator   string
}

var (
	reSig  = regexp.MustCompile(`(?i)"?\b(rsrp|rsrq|rssi|snr)\b"?\s*[:=]\s*"?(-?\d+(?:\.\d+)?)`)
	reTech = regexp.MustCompile(`(?i)access tech(?:nologies)?\s*:\s*(\S+)`)
	reOper = regexp.MustCompile(`(?i)operator name\s*:\s*(.+)`)
)

// ParseLTE understands mmcli and uqmi output. It returns nil when no signal
// figures were found, so routers without a modem produce no findings.
func ParseLTE(out string) *LTE {
	l := &LTE{}
	seen := map[string]bool{}
	for _, m := range reSig.FindAllStringSubmatch(out, -1) {
		k := strings.ToLower(m[1])
		if seen[k] {
			continue // first technology wins
		}
		seen[k] = true
		v, _ := strconv.ParseFloat(m[2], 64)
		switch k {
		case "rsrp":
			l.RSRP, l.HasRSRP = int(v), true
		case "rsrq":
			l.RSRQ = int(v)
		case "rssi":
			l.RSSI = int(v)
		case "snr":
			l.SNR, l.HasSNR = v, true
		}
	}
	if !l.HasRSRP && !l.HasSNR && !seen["rssi"] {
		return nil
	}
	if m := reTech.FindStringSubmatch(out); m != nil {
		l.Tech = strings.TrimSpace(m[1])
	}
	if m := reOper.FindStringSubmatch(out); m != nil {
		l.Operator = strings.TrimSpace(m[1])
	}
	return l
}

// Quality is a short word for the signal.
func (l LTE) Quality() string {
	switch {
	case !l.HasRSRP:
		return "unknown"
	case l.RSRP >= -90:
		return "excellent"
	case l.RSRP >= -105:
		return "good"
	case l.RSRP >= -115:
		return "weak"
	}
	return "very weak"
}

func ruleTemp(in Input) []Finding {
	f := in.Facts
	if !f.TempOK {
		return nil
	}
	switch {
	case f.TempC >= 85:
		return []Finding{{ID: "temp-high", Sev: Bad, Title: fmt.Sprintf("router is very hot (%.0f C)", f.TempC),
			Detail: "Sustained heat shortens the life of the router and can make it crash or reboot. Give it airflow, move it out of direct sun, and check for a runaway process."}}
	case f.TempC >= 75:
		return []Finding{{ID: "temp-warm", Sev: Warn, Title: fmt.Sprintf("router is running warm (%.0f C)", f.TempC),
			Detail: "Not dangerous yet, but check ventilation if it stays this warm."}}
	}
	return nil
}

// ruleWGStale looks at tunnels this router dials out (peers with an endpoint)
// and warns when they have not completed a handshake lately. Road-warrior
// peers that dial in are expected to be idle, so they are ignored.
func ruleWGStale(in Input) []Finding {
	f := in.Facts
	nw, ok := cfg(in, "network")
	if !ok || !f.WGKnown || f.Now == 0 {
		return nil
	}
	disabledIface := map[string]bool{}
	for _, s := range nw.Sections {
		if s.Type == "interface" && opt(s, "proto") == "wireguard" && opt(s, "disabled") == "1" {
			disabledIface[s.Name] = true
		}
	}
	last := map[string]int64{} // iface/peer key -> time
	for _, h := range f.Handshakes {
		last[h.Iface+"/"+h.Peer] = h.At
	}
	var out []Finding
	for _, s := range nw.Sections {
		if !strings.HasPrefix(s.Type, "wireguard_") || opt(s, "endpoint_host") == "" || opt(s, "disabled") == "1" {
			continue
		}
		iface := strings.TrimPrefix(s.Type, "wireguard_")
		key := opt(s, "public_key")
		if disabledIface[iface] || key == "" {
			continue
		}
		at, known := last[iface+"/"+key]
		desc := opt(s, "description")
		if desc == "" {
			desc = opt(s, "endpoint_host")
		}
		switch {
		case !known:
			continue // the interface is not up; the Network screen shows that
		case at == 0:
			out = append(out, Finding{ID: "wg-never-" + iface, Sev: Warn, Title: fmt.Sprintf("VPN %s has never connected to %s", iface, desc),
				Detail: "No handshake has completed. Check the endpoint address and port, the keys on both ends, and that the server is reachable from this connection."})
		case f.Now-at > 600:
			mins := (f.Now - at) / 60
			out = append(out, Finding{ID: "wg-stale-" + iface, Sev: Warn, Title: fmt.Sprintf("VPN %s: no handshake with %s for %d min", iface, desc, mins),
				Detail: "The tunnel is probably down. Traffic routed through it may be blocked (kill switch) or leaking, depending on your routing. Check the server and the router's uplink."})
		}
	}
	return out
}

func ruleLTE(in Input) []Finding {
	l := in.Facts.LTE
	if l == nil || !l.HasRSRP {
		return nil
	}
	switch {
	case l.RSRP < -115:
		return []Finding{{ID: "lte-weak", Sev: Warn, Title: fmt.Sprintf("cellular signal is very weak (RSRP %d dBm)", l.RSRP),
			Detail: "Expect slow speeds and drops. Try repositioning the router near a window, or use an external antenna."}}
	case l.RSRP < -105 || (l.HasSNR && l.SNR < 3):
		return []Finding{{ID: "lte-fair", Sev: Info, Title: fmt.Sprintf("cellular signal is weak (RSRP %d dBm, SNR %.0f dB)", l.RSRP, l.SNR),
			Detail: "Usable, but speeds will vary. Moving the router or adding an antenna usually helps."}}
	}
	return nil
}
