package health

import "fmt"

// ruleCellular is registered for every role but only has something to say
// about the uplink's address (CGNAT) when it is known.
func ruleCellular(in Input) []Finding {
	if !IsCGNAT(in.Facts.WanIP) {
		return nil
	}
	return []Finding{{
		ID: "cgnat", Sev: Info,
		Title: fmt.Sprintf("the internet side uses carrier-grade NAT (%s)", in.Facts.WanIP),
		Detail: "Addresses in 100.64.0.0/10 are shared by many customers, so nothing can connect IN to this router: port forwards and a WireGuard server will not be reachable from outside. " +
			"Use a VPN that this router connects OUT to (a WireGuard client to a relay or provider), or ask the carrier for a public IP.",
	}}
}

// ruleCellularRole adds the checks that only make sense on a SIM uplink.
func ruleCellularRole(in Input) []Finding {
	var out []Finding
	f := in.Facts
	if f.UpBytes > 0 {
		out = append(out, Finding{
			ID: "sim-usage", Sev: Info,
			Title:  fmt.Sprintf("data used on %s since boot: %s", f.UpDev, humanBytes(f.UpBytes)),
			Detail: "Counts everything the router sent and received on the SIM link since it last restarted (a reboot resets it). Check your plan's allowance with the carrier.",
		})
	}
	if wgServerExists(in) && !IsCGNAT(f.WanIP) {
		out = append(out, Finding{
			ID: "sim-inbound", Sev: Info,
			Title:  "this router has a WireGuard server on a SIM uplink",
			Detail: "Many carriers block inbound connections (even without CGNAT) and change the address often. Check that you can connect from outside, and use a dynamic DNS name if the address changes.",
		})
	}
	return out
}

func wgServerExists(in Input) bool {
	n, ok := cfg(in, "network")
	if !ok {
		return false
	}
	for _, s := range n.Sections {
		if s.Type == "interface" && opt(s, "proto") == "wireguard" && opt(s, "listen_port") != "" {
			return true
		}
	}
	return false
}

func humanBytes(n int64) string {
	const k = 1024.0
	v := float64(n)
	for _, u := range []string{"B", "KB", "MB", "GB", "TB"} {
		if v < k || u == "TB" {
			if u == "B" {
				return fmt.Sprintf("%d B", n)
			}
			return fmt.Sprintf("%.1f %s", v, u)
		}
		v /= k
	}
	return ""
}
