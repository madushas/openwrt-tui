package health

import (
	"fmt"
	"strings"
)

func init() { rules = append(rules, ruleDNSBypass, ruleDNSVPN) }

func dnsmasq(in Input) (map[string]string, []string, bool) {
	d, ok := cfg(in, "dhcp")
	if !ok {
		return nil, nil, false
	}
	for _, s := range d.Sections {
		if s.Type == "dnsmasq" {
			return map[string]string{"noresolv": opt(s, "noresolv")}, vals(s, "server"), true
		}
	}
	return nil, nil, false
}

// ruleDNSBypass: when the router filters or chooses DNS (ad blocking, custom
// upstream servers), any device that sets its own DNS server skips all of it.
// A firewall redirect of port 53 to the router prevents that for plain DNS.
func ruleDNSBypass(in Input) []Finding {
	fw, ok := cfg(in, "firewall")
	if !ok {
		return nil
	}
	_, servers, _ := dnsmasq(in)
	_, hasAdblock := cfg(in, "adblock")
	if !hasAdblock && len(servers) == 0 {
		return nil // nothing the router adds that could be bypassed
	}
	for _, s := range fw.Sections {
		if s.Type == "redirect" && opt(s, "enabled") != "0" && strings.EqualFold(opt(s, "target"), "DNAT") &&
			opt(s, "src_dport") == "53" && opt(s, "dest_ip") == "" {
			return nil // port 53 from clients is already sent to the router
		}
	}
	what := "custom upstream DNS servers"
	if hasAdblock {
		what = "ad blocking"
	}
	return []Finding{{
		ID: "dns-bypass", Sev: Info,
		Title: "devices can bypass the router's DNS",
		Detail: fmt.Sprintf("This router uses %s, but a device that sets its own DNS server (8.8.8.8, for example) skips it. "+
			"A firewall redirect of port 53 from the LAN to the router closes that for ordinary DNS. "+
			"Apps that use DNS over HTTPS or TLS still go around it; blocking those needs a separate filter list. If this is deliberate, press a.", what),
	}}
}

// ruleDNSVPN: with every connection sent through a VPN, DNS answered by the
// provider's resolvers (taken from the uplink) can reveal visited sites and
// can differ from what the VPN would give. It is enough that the router names
// its own upstream servers and ignores the uplink's.
func ruleDNSVPN(in Input) []Finding {
	n, ok := cfg(in, "network")
	if !ok {
		return nil
	}
	full := ""
	for _, s := range n.Sections {
		if strings.HasPrefix(s.Type, "wireguard_") && strings.Contains(strings.Join(vals(s, "allowed_ips"), " "), "0.0.0.0/0") {
			iface := strings.TrimPrefix(s.Type, "wireguard_")
			for _, i := range n.Sections {
				if i.Type == "interface" && i.Name == iface && opt(i, "proto") == "wireguard" && opt(i, "disabled") != "1" {
					// route_allowed_ips is per peer; without it the VPN only takes traffic that policy routing sends to it
					if opt(s, "route_allowed_ips") == "1" {
						full = iface
					}
				}
			}
		}
	}
	if full == "" {
		return nil
	}
	nr, servers, ok := dnsmasq(in)
	if ok && nr["noresolv"] == "1" && len(servers) > 0 {
		return nil
	}
	return []Finding{{
		ID: "dns-vpn-leak", Sev: Warn,
		Title: fmt.Sprintf("DNS may be answered outside the VPN '%s'", full),
		Detail: "All traffic goes through the VPN, but the router has no fixed upstream DNS (or still adds the uplink's resolvers), so lookups can go to the internet provider and reveal the sites you visit. " +
			"Set upstream DNS (Tasks > Upstream DNS) with 'ignore resolv file' on, preferably to the VPN provider's resolver.",
	}}
}
