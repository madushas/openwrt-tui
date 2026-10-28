// Package metrics samples live router statistics with one read-only shell
// script and turns two samples into rates (CPU %, bytes per second). Parsing
// is tolerant: a missing file on the router just leaves that part empty.
package metrics

import (
	"encoding/json"
	"errors"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Script prints every section in one round trip. It only reads files and runs
// read-only commands. "##owrt-metrics" is the marker line tests and the demo
// router match on.
const Script = `
echo '##owrt-metrics'
echo '##load'; cat /proc/loadavg 2>/dev/null
echo '##uptime'; cat /proc/uptime 2>/dev/null
echo '##time'; date +%s 2>/dev/null
echo '##cores'; grep -c '^processor' /proc/cpuinfo 2>/dev/null
echo '##stat'; head -n1 /proc/stat 2>/dev/null
echo '##mem'; grep -E '^(MemTotal|MemFree|MemAvailable|Buffers|Cached|SwapTotal|SwapFree):' /proc/meminfo 2>/dev/null
echo '##temp'; cat /sys/class/thermal/thermal_zone*/temp 2>/dev/null | head -n1
echo '##net'; cat /proc/net/dev 2>/dev/null
echo '##ct'; cat /proc/sys/net/netfilter/nf_conntrack_count /proc/sys/net/netfilter/nf_conntrack_max 2>/dev/null
echo '##ubus'; ubus call network.interface dump 2>/dev/null
echo '##leases'; cat /tmp/dhcp.leases 2>/dev/null
echo '##neigh'; ip -4 neigh show 2>/dev/null
echo '##wifi'
for d in /sys/class/net/*; do
  n=${d##*/}
  [ -d "$d/wireless" ] || [ -d "$d/phy80211" ] || continue
  echo "dev $n"; iwinfo "$n" info 2>/dev/null | head -n1; iwinfo "$n" assoclist 2>/dev/null
done
echo '##end'
`

// Sample is one reading.
type Sample struct {
	At       float64 // router uptime in seconds when read; the clock for rates
	Epoch    int64
	Load     [3]float64
	Cores    int
	CPUTotal uint64 // jiffies, all states
	CPUIdle  uint64 // idle + iowait
	MemTotal int64  // KiB
	MemAvail int64
	SwapTotal,
	SwapFree int64
	TempC    float64
	HasTemp  bool
	Net      []Counters
	CtCount  int
	CtMax    int
	Ifaces   []Iface
	Leases   []Lease
	Stations []Station
	Neigh    []Neigh
}

// Counters are cumulative interface counters.
type Counters struct {
	Name             string
	RxBytes, TxBytes uint64
	RxPkts, TxPkts   uint64
}

// Iface is a logical OpenWrt interface (lan, wan, ...) from netifd.
type Iface struct {
	Name, Proto, Device string
	Up                  bool
	Uptime              int
	Addrs               []string
	Gateway             string
	DNS                 []string
	Default             bool // carries the default route
}

// Lease is one DHCP lease.
type Lease struct {
	Expiry int64 // epoch seconds; 0 means it never expires
	MAC    string
	IP     string
	Host   string
}

// Neigh is one ARP table entry; State is the kernel's view of whether the
// device answered recently (REACHABLE, DELAY, PROBE, STALE, ...).
type Neigh struct{ IP, MAC, Dev, State string }

// Station is a connected Wi-Fi client.
type Station struct {
	Dev, ESSID, MAC string
	Signal          int // dBm
}

// Parse reads the output of Script.
func Parse(out string) (Sample, error) {
	secs := map[string][]string{}
	cur := ""
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.HasPrefix(line, "##") {
			cur = strings.TrimPrefix(line, "##")
			continue
		}
		if cur != "" {
			secs[cur] = append(secs[cur], line)
		}
	}
	var s Sample
	if len(secs) == 0 {
		return s, errors.New("no metrics returned")
	}
	if f := fields(first(secs["uptime"])); len(f) > 0 {
		s.At, _ = strconv.ParseFloat(f[0], 64)
	}
	if s.At <= 0 {
		return s, errors.New("router did not report /proc/uptime")
	}
	s.Epoch, _ = strconv.ParseInt(strings.TrimSpace(first(secs["time"])), 10, 64)
	if f := fields(first(secs["load"])); len(f) >= 3 {
		for i := 0; i < 3; i++ {
			s.Load[i], _ = strconv.ParseFloat(f[i], 64)
		}
	}
	s.Cores, _ = strconv.Atoi(strings.TrimSpace(first(secs["cores"])))
	if s.Cores < 1 {
		s.Cores = 1
	}
	parseStat(&s, first(secs["stat"]))
	parseMem(&s, secs["mem"])
	if t, err := strconv.ParseFloat(strings.TrimSpace(first(secs["temp"])), 64); err == nil && t != 0 {
		s.TempC, s.HasTemp = t/1000, true
	}
	s.Net = parseNetDev(secs["net"])
	ct := nonEmpty(secs["ct"])
	if len(ct) >= 2 {
		s.CtCount, _ = strconv.Atoi(strings.TrimSpace(ct[0]))
		s.CtMax, _ = strconv.Atoi(strings.TrimSpace(ct[1]))
	}
	s.Ifaces = parseUbus(strings.Join(secs["ubus"], "\n"))
	s.Leases = parseLeases(secs["leases"])
	s.Neigh = parseNeigh(secs["neigh"])
	s.Stations = parseWifi(secs["wifi"])
	return s, nil
}

func first(l []string) string {
	for _, s := range l {
		if strings.TrimSpace(s) != "" {
			return s
		}
	}
	return ""
}

func nonEmpty(l []string) []string {
	var o []string
	for _, s := range l {
		if strings.TrimSpace(s) != "" {
			o = append(o, s)
		}
	}
	return o
}

func fields(s string) []string { return strings.Fields(s) }

func parseStat(s *Sample, line string) {
	f := fields(line)
	if len(f) < 5 || f[0] != "cpu" {
		return
	}
	var v [10]uint64
	for i := 1; i < len(f) && i <= 10; i++ {
		v[i-1], _ = strconv.ParseUint(f[i], 10, 64)
	}
	// user nice system idle iowait irq softirq steal (guest is already in user)
	for i := 0; i < 8; i++ {
		s.CPUTotal += v[i]
	}
	s.CPUIdle = v[3] + v[4]
}

func parseMem(s *Sample, lines []string) {
	m := map[string]int64{}
	for _, l := range lines {
		k, v, ok := strings.Cut(l, ":")
		if !ok {
			continue
		}
		f := fields(v)
		if len(f) > 0 {
			m[k], _ = strconv.ParseInt(f[0], 10, 64)
		}
	}
	s.MemTotal = m["MemTotal"]
	if a, ok := m["MemAvailable"]; ok {
		s.MemAvail = a
	} else {
		s.MemAvail = m["MemFree"] + m["Buffers"] + m["Cached"]
	}
	s.SwapTotal, s.SwapFree = m["SwapTotal"], m["SwapFree"]
}

func parseNetDev(lines []string) []Counters {
	var out []Counters
	for _, l := range lines {
		name, rest, ok := strings.Cut(l, ":")
		name = strings.TrimSpace(name)
		if !ok || name == "" || name == "lo" {
			continue
		}
		f := fields(rest)
		if len(f) < 10 {
			continue
		}
		p := func(i int) uint64 { v, _ := strconv.ParseUint(f[i], 10, 64); return v }
		out = append(out, Counters{Name: name, RxBytes: p(0), RxPkts: p(1), TxBytes: p(8), TxPkts: p(9)})
	}
	return out
}

type ubusDump struct {
	Interface []struct {
		Interface string `json:"interface"`
		Up        bool   `json:"up"`
		Proto     string `json:"proto"`
		Device    string `json:"device"`
		L3Device  string `json:"l3_device"`
		Uptime    int    `json:"uptime"`
		IPv4      []struct {
			Address string `json:"address"`
			Mask    int    `json:"mask"`
		} `json:"ipv4-address"`
		IPv6 []struct {
			Address string `json:"address"`
			Mask    int    `json:"mask"`
		} `json:"ipv6-address"`
		Route []struct {
			Target  string `json:"target"`
			Mask    int    `json:"mask"`
			Nexthop string `json:"nexthop"`
		} `json:"route"`
		DNS []string `json:"dns-server"`
	} `json:"interface"`
}

func parseUbus(js string) []Iface {
	var d ubusDump
	if strings.TrimSpace(js) == "" || json.Unmarshal([]byte(js), &d) != nil {
		return nil
	}
	var out []Iface
	for _, i := range d.Interface {
		if i.Interface == "loopback" {
			continue
		}
		f := Iface{Name: i.Interface, Proto: i.Proto, Up: i.Up, Uptime: i.Uptime, DNS: i.DNS, Device: i.L3Device}
		if f.Device == "" {
			f.Device = i.Device
		}
		for _, a := range i.IPv4 {
			f.Addrs = append(f.Addrs, a.Address+"/"+strconv.Itoa(a.Mask))
		}
		for _, a := range i.IPv6 {
			f.Addrs = append(f.Addrs, a.Address+"/"+strconv.Itoa(a.Mask))
		}
		for _, r := range i.Route {
			if r.Mask == 0 && (r.Target == "0.0.0.0" || r.Target == "::") {
				f.Default = true
				if f.Gateway == "" {
					f.Gateway = r.Nexthop
				}
			}
		}
		out = append(out, f)
	}
	return out
}

func parseLeases(lines []string) []Lease {
	var out []Lease
	for _, l := range lines {
		f := fields(l)
		if len(f) < 4 {
			continue
		}
		exp, _ := strconv.ParseInt(f[0], 10, 64)
		h := f[3]
		if h == "*" {
			h = ""
		}
		out = append(out, Lease{Expiry: exp, MAC: strings.ToLower(f[1]), IP: f[2], Host: h})
	}
	return out
}

var (
	assocRe = regexp.MustCompile(`^([0-9A-Fa-f]{2}(?::[0-9A-Fa-f]{2}){5})\s+(-?\d+) dBm`)
	essidRe = regexp.MustCompile(`ESSID:\s*"([^"]*)"`)
)

func parseWifi(lines []string) []Station {
	var out []Station
	dev, essid := "", ""
	for _, l := range lines {
		t := strings.TrimSpace(l)
		switch {
		case strings.HasPrefix(l, "dev "):
			dev, essid = strings.TrimSpace(strings.TrimPrefix(l, "dev ")), ""
		case essidRe.MatchString(l):
			essid = essidRe.FindStringSubmatch(l)[1]
		default:
			if m := assocRe.FindStringSubmatch(t); m != nil {
				sig, _ := strconv.Atoi(m[2])
				out = append(out, Station{Dev: dev, ESSID: essid, MAC: strings.ToLower(m[1]), Signal: sig})
			}
		}
	}
	return out
}

// ActiveLeases returns leases that are still valid at the sample time.
func (s Sample) ActiveLeases() []Lease {
	var out []Lease
	for _, l := range s.Leases {
		if l.Expiry == 0 || s.Epoch == 0 || l.Expiry > s.Epoch {
			out = append(out, l)
		}
	}
	return out
}

// Bars maps a signal in dBm to 1..4 bars.
func Bars(dbm int) int {
	switch {
	case dbm >= -55:
		return 4
	case dbm >= -67:
		return 3
	case dbm >= -75:
		return 2
	}
	return 1
}

// MemUsedPct is memory in use, counting reclaimable cache as free.
func (s Sample) MemUsedPct() float64 {
	if s.MemTotal <= 0 {
		return 0
	}
	return 100 * float64(s.MemTotal-s.MemAvail) / float64(s.MemTotal)
}

// ---- rates ----

// Rate is bytes and packets per second.
type Rate struct{ RxBps, TxBps float64 }

// Rates are the changes between two samples.
type Rates struct {
	CPUPct float64
	Net    map[string]Rate
}

// Delta computes rates. ok is false when the samples cannot be compared (same
// instant, or the router rebooted in between).
func Delta(prev, cur Sample) (r Rates, ok bool) {
	dt := cur.At - prev.At
	if dt <= 0 {
		return r, false
	}
	if dTotal := float64(cur.CPUTotal) - float64(prev.CPUTotal); dTotal > 0 && cur.CPUTotal >= prev.CPUTotal {
		busy := dTotal - (float64(cur.CPUIdle) - float64(prev.CPUIdle))
		r.CPUPct = clamp(100*busy/dTotal, 0, 100)
	}
	r.Net = map[string]Rate{}
	old := map[string]Counters{}
	for _, c := range prev.Net {
		old[c.Name] = c
	}
	for _, c := range cur.Net {
		p, found := old[c.Name]
		if !found {
			continue
		}
		var x Rate
		if c.RxBytes >= p.RxBytes { // a lower value means the counter reset
			x.RxBps = float64(c.RxBytes-p.RxBytes) / dt
		}
		if c.TxBytes >= p.TxBytes {
			x.TxBps = float64(c.TxBytes-p.TxBytes) / dt
		}
		r.Net[c.Name] = x
	}
	return r, true
}

func clamp(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// ---- history ----

// Series keeps the last Cap values.
type Series struct {
	Cap  int
	vals []float64
}

// Push appends v, dropping the oldest value when full.
func (s *Series) Push(v float64) {
	if s.Cap <= 0 {
		s.Cap = 120
	}
	s.vals = append(s.vals, v)
	if len(s.vals) > s.Cap {
		s.vals = s.vals[len(s.vals)-s.Cap:]
	}
}

// Values returns a copy, oldest first.
func (s *Series) Values() []float64 { return append([]float64(nil), s.vals...) }

// Last returns the newest value or 0.
func (s *Series) Last() float64 {
	if len(s.vals) == 0 {
		return 0
	}
	return s.vals[len(s.vals)-1]
}

// Max returns the largest value or 0.
func (s *Series) Max() float64 {
	m := 0.0
	for _, v := range s.vals {
		if v > m {
			m = v
		}
	}
	return m
}

// SortedNet returns interface names ordered by combined rate, busiest first.
func SortedNet(r Rates) []string {
	var n []string
	for k := range r.Net {
		n = append(n, k)
	}
	sort.Slice(n, func(i, j int) bool {
		a, b := r.Net[n[i]], r.Net[n[j]]
		if a.RxBps+a.TxBps != b.RxBps+b.TxBps {
			return a.RxBps+a.TxBps > b.RxBps+b.TxBps
		}
		return n[i] < n[j]
	})
	return n
}

// parseNeigh reads "ip -4 neigh show". Entries without a link-layer address
// (FAILED, INCOMPLETE) say nothing about a device and are dropped.
func parseNeigh(lines []string) []Neigh {
	var out []Neigh
	for _, l := range lines {
		f := strings.Fields(l)
		if len(f) < 4 {
			continue
		}
		n := Neigh{IP: f[0], State: f[len(f)-1]}
		for i := 1; i+1 < len(f); i++ {
			switch f[i] {
			case "dev":
				n.Dev = f[i+1]
			case "lladdr":
				n.MAC = strings.ToLower(f[i+1])
			}
		}
		if n.MAC != "" {
			out = append(out, n)
		}
	}
	return out
}
