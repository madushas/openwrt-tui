package tui

import (
	"context"
	"fmt"
	"net/netip"
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/madushakv/owrt-tui/internal/metrics"
)

const histLen = 240

var intervals = []time.Duration{time.Second, 2 * time.Second, 5 * time.Second, 10 * time.Second}

type dashboard struct {
	e *env

	active, paused, looping bool
	interval                int // index into intervals
	bits                    bool

	have      bool
	prev, cur metrics.Sample
	rates     metrics.Rates
	haveRate  bool
	lastOK    time.Time
	err       error
	fails     int

	cpu, mem metrics.Series
	rx, tx   map[string]*metrics.Series
	sel      string // device whose traffic is graphed; "" = automatic
	scroll   int    // first visible client row
}

func newDashboard(e *env) *dashboard {
	return &dashboard{e: e, interval: 1, bits: true,
		cpu: metrics.Series{Cap: histLen}, mem: metrics.Series{Cap: histLen},
		rx: map[string]*metrics.Series{}, tx: map[string]*metrics.Series{}}
}

func (d *dashboard) Title() string    { return "Dashboard" }
func (d *dashboard) Capturing() bool  { return false }
func (d *dashboard) Borderless() bool { return true }
func (d *dashboard) Init() tea.Cmd    { return nil }
func (d *dashboard) Keys() string {
	return "i interface  p pause  +/- speed  u bits/bytes  j/k clients  R reboot"
}

func (d *dashboard) Status() string {
	switch {
	case d.paused:
		return "❚❚ paused"
	case d.err != nil:
		return "○ no data"
	case d.have:
		return fmt.Sprintf("● live %s", intervals[d.interval])
	}
	return "starting"
}

type (
	sampleMsg struct {
		s   metrics.Sample
		err error
	}
	tickMsg       struct{}
	rebootDoneMsg struct{ err error }
)

// SetActive is called when the screen is shown or hidden. Polling only runs
// while the dashboard is visible, so a hidden dashboard costs the router nothing.
func (d *dashboard) SetActive(on bool) tea.Cmd {
	d.active = on
	return d.kick()
}

func (d *dashboard) kick() tea.Cmd {
	if d.looping || d.paused || !d.active {
		return nil
	}
	d.looping = true
	return d.sample()
}

func (d *dashboard) sample() tea.Cmd {
	return d.e.call(func(ctx context.Context) tea.Msg {
		s, err := d.e.S.Sample(ctx)
		return sampleMsg{s, err}
	})
}

func (d *dashboard) next() tea.Cmd {
	if !d.active || d.paused {
		d.looping = false
		return nil
	}
	every := intervals[d.interval]
	if d.fails > 0 { // back off while the router is unreachable
		every = every * time.Duration(1+min(d.fails, 4))
	}
	return tea.Tick(every, func(time.Time) tea.Msg { return tickMsg{} })
}

func (d *dashboard) Update(msg tea.Msg) (screen, tea.Cmd) {
	switch m := msg.(type) {
	case sampleMsg:
		if m.err != nil {
			d.err, d.fails = m.err, d.fails+1
			return d, d.next()
		}
		d.err, d.fails, d.lastOK = nil, 0, time.Now()
		d.ingest(m.s)
		return d, d.next()
	case tickMsg:
		if !d.active || d.paused {
			d.looping = false
			return d, nil
		}
		return d, d.sample()
	case rebootDoneMsg:
		if m.err != nil {
			return d, toastErr(m.err)
		}
		return d, toast("reboot sent; the connection will drop and the router is back in about a minute")
	case tea.KeyPressMsg:
		return d.key(m.String())
	}
	return d, nil
}

func (d *dashboard) ingest(s metrics.Sample) {
	if d.have {
		if r, ok := metrics.Delta(d.cur, s); ok {
			d.rates, d.haveRate = r, true
			d.cpu.Push(r.CPUPct)
			for name, x := range r.Net {
				if d.rx[name] == nil {
					d.rx[name], d.tx[name] = &metrics.Series{Cap: histLen}, &metrics.Series{Cap: histLen}
				}
				d.rx[name].Push(x.RxBps)
				d.tx[name].Push(x.TxBps)
			}
		} else {
			d.haveRate = false // rebooted: start the history over
			d.cpu, d.rx, d.tx = metrics.Series{Cap: histLen}, map[string]*metrics.Series{}, map[string]*metrics.Series{}
		}
	}
	d.mem.Push(s.MemUsedPct())
	d.prev, d.cur, d.have = d.cur, s, true
}

func (d *dashboard) key(k string) (screen, tea.Cmd) {
	switch k {
	case "p":
		d.paused = !d.paused
		return d, d.kick()
	case "+", "=":
		d.interval = clampInt(d.interval-1, 0, len(intervals)-1)
	case "-", "_":
		d.interval = clampInt(d.interval+1, 0, len(intervals)-1)
	case "u":
		d.bits = !d.bits
	case "i":
		d.cycleIface()
	case "j", "down":
		d.scroll++
	case "k", "up":
		d.scroll = max0(d.scroll - 1)
	case "R":
		if d.e.S.ReadOnly {
			return d, toast("read-only mode: start with -write to reboot")
		}
		return d, func() tea.Msg {
			return askMsg{Prompt: "Reboot " + d.e.S.T.Describe() + " now?", Danger: true, Yes: func() tea.Cmd {
				return d.e.call(func(ctx context.Context) tea.Msg { return rebootDoneMsg{d.e.S.Reboot(ctx)} })
			}}
		}
	}
	return d, nil
}

// devices lists network devices that have counters, busiest first.
func (d *dashboard) devices() []string {
	names := metrics.SortedNet(d.rates)
	if len(names) == 0 {
		for _, c := range d.cur.Net {
			names = append(names, c.Name)
		}
		sort.Strings(names)
	}
	return names
}

func (d *dashboard) cycleIface() {
	names := d.devices()
	if len(names) == 0 {
		return
	}
	cur := d.graphDev()
	for i, n := range names {
		if n == cur {
			d.sel = names[(i+1)%len(names)]
			return
		}
	}
	d.sel = names[0]
}

// graphDev picks the device to graph: the user's choice, else the one that
// carries the default route, else the busiest.
func (d *dashboard) graphDev() string {
	if d.sel != "" {
		return d.sel
	}
	for _, f := range d.cur.Ifaces {
		if f.Default && f.Device != "" {
			return f.Device
		}
	}
	if n := d.devices(); len(n) > 0 {
		return n[0]
	}
	return ""
}

// ---- view ----

func (d *dashboard) View(w, h int) string {
	info := d.e.S.Info
	if !d.have {
		msg := "\n  waiting for the first reading from " + d.e.S.T.Describe() + " ..."
		if d.err != nil {
			msg = "\n  " + sBad.Render("no data: "+d.err.Error()) + "\n  " + sDim.Render("retrying; the router may be rebooting or unreachable")
		}
		return msg
	}
	// Errors come first: they say the numbers below are stale.
	var banner []string
	if d.err != nil {
		stale := time.Since(d.lastOK).Round(time.Second)
		banner = append(banner, sBad.Render(fmt.Sprintf(" no data for %s: %s", stale, d.err.Error())))
	}
	for _, a := range info.Advisories() {
		st := sWarn
		if a.Level == "info" {
			st = sDim
		}
		banner = append(banner, st.Render(" ! "+a.Text))
	}
	if w < 60 || h < 14 {
		return strings.Join(append(banner, d.compact(w)...), "\n")
	}
	limit := 1
	if h >= 27 {
		limit = 2
	}
	if len(banner) > limit {
		banner = banner[:limit]
	}
	body := strings.Join(banner, "\n")
	gh := h - len(banner)

	var rows []string
	switch {
	case gh >= 27:
		rowA := 8
		rowB := clampInt((gh-rowA)*4/10, 10, 14)
		rowC := gh - rowA - rowB
		rows = []string{d.rowTop(w, rowA), d.rowNet(w, rowB), d.rowClients(w, rowC)}
	case gh >= 17:
		// Not tall enough for all three rows: keep what changes (traffic) and
		// who is connected (clients); the static system facts are on the Menu
		// header and the Overview facts are one resize away.
		rowB := clampInt(gh*5/9, 8, 12)
		rows = []string{d.rowNet(w, rowB), d.rowClients(w, gh-rowB)}
	default:
		rows = []string{d.rowNet(w, gh)}
	}
	if body != "" {
		return body + "\n" + strings.Join(rows, "\n")
	}
	return strings.Join(rows, "\n")
}

// split divides w columns into n parts (the last takes the remainder).
func split(w, n int) []int {
	out := make([]int, n)
	for i := range out {
		out[i] = w / n
	}
	out[n-1] += w - (w/n)*n
	return out
}

func join(panels ...string) string {
	cols := make([][]string, len(panels))
	hmax := 0
	for i, p := range panels {
		cols[i] = strings.Split(p, "\n")
		if len(cols[i]) > hmax {
			hmax = len(cols[i])
		}
	}
	var out []string
	for r := 0; r < hmax; r++ {
		var b strings.Builder
		for i := range cols {
			if r < len(cols[i]) {
				b.WriteString(cols[i][r])
			}
		}
		out = append(out, b.String())
	}
	return strings.Join(out, "\n")
}

func (d *dashboard) rowTop(w, h int) string {
	var ws []int
	if w >= 100 {
		ws = split(w, 3)
		return join(d.panelSystem(ws[0], h), d.panelCPU(ws[1], h), d.panelResources(ws[2], h))
	}
	ws = split(w, 2)
	return join(d.panelSystem(ws[0], h), d.panelResources(ws[1], h))
}

func (d *dashboard) rowNet(w, h int) string {
	l := w * 2 / 5
	return join(d.panelNetwork(l, h), d.panelTraffic(w-l, h))
}

func (d *dashboard) rowClients(w, h int) string {
	l := w * 2 / 5
	return join(d.panelDevices(l, h), d.panelClients(w-l, h))
}

func kv(k, v string) string { return sDim.Render(fmt.Sprintf("%-9s", k)) + v }

func (d *dashboard) panelSystem(w, h int) string {
	i := d.e.S.Info
	rel := strings.TrimSpace(i.Distro + " " + i.Version)
	lines := []string{
		kv("Host", sBold.Render(i.Hostname)),
		kv("Model", i.Model),
		kv("Release", rel),
		kv("Uptime", uptime(int(d.cur.At))),
		kv("Kernel", i.Kernel),
		kv("Stack", strings.TrimSpace(i.PkgMgr+" · "+i.Firewall)),
	}
	return panel("System", "", strings.Join(lines, "\n"), w, h, false)
}

func (d *dashboard) panelCPU(w, h int) string {
	c := d.cur
	head := fmt.Sprintf("%s  %s", sBold.Render(fmt.Sprintf("%3.0f%%", d.cpu.Last())), sDim.Render(fmt.Sprintf("load %.2f %.2f %.2f", c.Load[0], c.Load[1], c.Load[2])))
	body := head + "\n" + graph(d.cpu.Values(), w-2, max0(h-3), 100, sRx)
	return panel("CPU", fmt.Sprintf("%d cores", c.Cores), body, w, h, false)
}

func (d *dashboard) panelResources(w, h int) string {
	i := d.e.S.Info
	// Leave room for the longest text after the bar ("2.5 MiB free, low").
	bw := clampInt(w-2-8-1-1-20, 6, 20)
	row := func(label string, pct float64, extra string) string {
		return fmt.Sprintf("%s %s %s", sDim.Render(fmt.Sprintf("%-8s", label)), bar(pct, bw), extra)
	}
	c := d.cur
	lines := []string{row("RAM", c.MemUsedPct(), sDim.Render(fmt.Sprintf("%.0f%% of %s", c.MemUsedPct(), kib(c.MemTotal))))}
	if i.OverlayKB.Total > 0 {
		p := 100 * float64(i.OverlayKB.Used) / float64(i.OverlayKB.Total)
		extra := sDim.Render(fmt.Sprintf("%s free", kib(i.OverlayKB.Avail)))
		if i.OverlayKB.Avail < 4096 {
			extra = sWarn.Render(fmt.Sprintf("%s free, low", kib(i.OverlayKB.Avail)))
		}
		lines = append(lines, row("Flash", p, extra))
	}
	if c.CtMax > 0 {
		p := 100 * float64(c.CtCount) / float64(c.CtMax)
		lines = append(lines, row("Conntrk", p, sDim.Render(fmt.Sprintf("%d / %d", c.CtCount, c.CtMax))))
	}
	if c.SwapTotal > 0 {
		p := 100 * float64(c.SwapTotal-c.SwapFree) / float64(c.SwapTotal)
		lines = append(lines, row("Swap", p, sDim.Render(kib(c.SwapTotal))))
	}
	if c.HasTemp {
		st := sGood
		switch {
		case c.TempC >= 80:
			st = sBad
		case c.TempC >= 70:
			st = sWarn
		}
		lines = append(lines, sDim.Render(fmt.Sprintf("%-8s ", "Temp"))+st.Render(fmt.Sprintf("%.0f°C", c.TempC)))
	}
	if len(d.mem.Values()) > 3 && h >= 9 {
		lines = append(lines, sDim.Render(fmt.Sprintf("%-8s ", "RAM hist"))+sTx.Render(spark(d.mem.Values(), bw, 100)))
	}
	return panel("Resources", "", strings.Join(lines, "\n"), w, h, false)
}

// orderedIfaces puts the interface that carries the default route first, then
// lan, then the other interfaces that are up. Interfaces that are down are
// returned separately so they can be summarised on one line.
func orderedIfaces(all []metrics.Iface) (up, down []metrics.Iface) {
	for _, f := range all {
		if f.Up {
			up = append(up, f)
		} else {
			down = append(down, f)
		}
	}
	rank := func(f metrics.Iface) int {
		switch {
		case f.Default:
			return 0
		case f.Name == "lan":
			return 1
		}
		return 2
	}
	sort.SliceStable(up, func(i, j int) bool {
		if ri, rj := rank(up[i]), rank(up[j]); ri != rj {
			return ri < rj
		}
		return up[i].Name < up[j].Name
	})
	return up, down
}

func (d *dashboard) panelNetwork(w, h int) string {
	var lines []string
	up, down := orderedIfaces(d.cur.Ifaces)
	for _, f := range up {
		name := f.Name
		if f.Default {
			name += " ★"
		}
		lines = append(lines, chipGood.Render("UP")+" "+sBold.Render(name)+sDim.Render("  "+f.Proto+"  "+f.Device))
		detail := ""
		if len(f.Addrs) > 0 {
			detail = f.Addrs[0]
		} else {
			detail = sDim.Render("no address")
		}
		if f.Uptime > 0 {
			detail += sDim.Render("  · up " + uptime(f.Uptime))
		}
		lines = append(lines, "   "+detail)
		if f.Default {
			var extra []string
			if f.Gateway != "" {
				extra = append(extra, "gw "+f.Gateway)
			}
			if len(f.DNS) > 0 {
				extra = append(extra, "dns "+strings.Join(f.DNS, ","))
			}
			if len(extra) > 0 {
				lines = append(lines, "   "+sDim.Render(strings.Join(extra, "  ")))
			}
		}
	}
	if len(down) > 0 {
		var names []string
		for _, f := range down {
			names = append(names, f.Name)
		}
		lines = append(lines, chipBad.Render("DOWN")+" "+sDim.Render(strings.Join(names, ", ")))
	}
	if len(lines) == 0 {
		lines = []string{sDim.Render("no interface data (is ubus available?)")}
	}
	return panel("Network", "", strings.Join(lines, "\n"), w, h, false)
}

func (d *dashboard) panelTraffic(w, h int) string {
	dev := d.graphDev()
	if dev == "" || !d.haveRate {
		return panel("Traffic", "", sDim.Render(" measuring..."), w, h, false)
	}
	rx, tx := d.rx[dev], d.tx[dev]
	if rx == nil {
		return panel("Traffic", dev, sDim.Render(" no counters for "+dev), w, h, false)
	}
	gw := w - 2
	// Download and upload each get their own scale (with a 1 Mb/s floor so
	// idle noise does not fill the graph); the peak is printed beside each.
	const floor = 125000.0
	inner := h - 2
	gh := max0((inner - 2) / 2)
	var b strings.Builder
	b.WriteString(sRx.Render("▼ down ") + sBold.Render(rate(rx.Last(), d.bits)) + sDim.Render("   peak "+rate(rx.Max(), d.bits)) + "\n")
	b.WriteString(graph(rx.Values(), gw, gh, maxf(rx.Max(), floor), sRx) + "\n")
	b.WriteString(sTx.Render("▲ up   ") + sBold.Render(rate(tx.Last(), d.bits)) + sDim.Render("   peak "+rate(tx.Max(), d.bits)) + "\n")
	b.WriteString(graph(tx.Values(), gw, gh, maxf(tx.Max(), floor), sTx))
	return panel("Traffic", dev, b.String(), w, h, false)
}

func maxf(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

func (d *dashboard) panelDevices(w, h int) string {
	var lines []string
	lines = append(lines, sDim.Render(fmt.Sprintf("%-10s %11s %11s", "DEVICE", "DOWN", "UP")))
	idle := 0
	counters := map[string]metrics.Counters{}
	for _, c := range d.cur.Net {
		counters[c.Name] = c
	}
	for _, n := range d.devices() {
		// Ports with nothing plugged in (counters all zero) only add noise.
		if c, ok := counters[n]; ok && c.RxBytes+c.TxBytes == 0 && n != d.graphDev() {
			idle++
			continue
		}
		x := d.rates.Net[n]
		sp := ""
		if r := d.rx[n]; r != nil && w >= 54 {
			sp = " " + sRx.Render(spark(r.Values(), w-2-35, 0))
		}
		mark := " "
		if n == d.graphDev() {
			mark = sAccent.Render("▸")
		}
		lines = append(lines, fmt.Sprintf("%s%-9s %11s %11s%s", mark, clip(n, 9), rate(x.RxBps, d.bits), rate(x.TxBps, d.bits), sp))
	}
	if idle > 0 {
		lines = append(lines, sDim.Render(fmt.Sprintf(" +%d with no traffic", idle)))
	}
	return panel("Devices", "i selects", strings.Join(lines, "\n"), w, h, false)
}

type clientRow struct {
	name, ip, mac string
	signal        int
	wifi, static  bool
	essid         string
	state         string // ARP state of a client that is not on Wi-Fi; "" = no entry
	haveARP       bool   // the router returned an ARP table at all
}

func (d *dashboard) clients() []clientRow {
	byMAC := map[string]metrics.Station{}
	for _, s := range d.cur.Stations {
		byMAC[s.MAC] = s
	}
	arp := map[string]string{}
	for _, n := range d.cur.Neigh {
		arp[n.MAC] = n.State
	}
	var out []clientRow
	seen := map[string]bool{}
	for _, l := range d.cur.ActiveLeases() {
		r := clientRow{name: l.Host, ip: l.IP, mac: l.MAC, static: l.Expiry == 0}
		if r.name == "" {
			r.name = "(unnamed)"
		}
		if s, ok := byMAC[l.MAC]; ok {
			r.wifi, r.signal, r.essid = true, s.Signal, s.ESSID
		}
		r.state, r.haveARP = arp[l.MAC], len(arp) > 0
		seen[l.MAC] = true
		out = append(out, r)
	}
	for _, s := range d.cur.Stations { // associated but no lease (static IP)
		if !seen[s.MAC] {
			out = append(out, clientRow{name: s.MAC, mac: s.MAC, wifi: true, signal: s.Signal, essid: s.ESSID})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return ipLess(out[i].ip, out[j].ip, out[i].name, out[j].name) })
	return out
}

func (d *dashboard) panelClients(w, h int) string {
	cl := d.clients()
	nw := 0
	for _, c := range cl {
		if c.wifi {
			nw++
		}
	}
	rowsAvail := max0(h - 2)
	maxScroll := max0(len(cl) - rowsAvail)
	d.scroll = clampInt(d.scroll, 0, maxScroll)
	var lines []string
	end := min(len(cl), d.scroll+rowsAvail)
	for _, c := range cl[d.scroll:end] {
		var sig string
		switch {
		case c.wifi:
			sig = bars(metrics.Bars(c.signal)) + sDim.Render(fmt.Sprintf(" %4d dBm", c.signal))
			if avail := w - 2 - 51 - 1; c.essid != "" && avail >= 5 {
				sig += " " + sDim.Render(clip(c.essid, min(avail, 18)))
			}
		case c.state == "REACHABLE" || c.state == "DELAY" || c.state == "PROBE":
			sig = sGood.Render("●") + sDim.Render(" wired / other")
		case c.state != "":
			sig = sDim.Render("○ idle")
		case !c.haveARP: // no ARP table to judge by
			sig = sDim.Render("not on Wi-Fi")
		default:
			sig = sDim.Render("– offline")
		}
		name := c.name
		if c.static {
			name += sDim.Render(" (static)")
		}
		lines = append(lines, fmt.Sprintf("%s  %-15s %s", padVisible(clip(name, 20), 20), c.ip, sig))
	}
	if len(cl) == 0 {
		lines = []string{sDim.Render(" no DHCP leases or Wi-Fi clients found")}
	}
	right := fmt.Sprintf("%d clients · %d Wi-Fi", len(cl), nw)
	return panel("Clients", right, strings.Join(lines, "\n"), w, h, false)
}

// compact is the dashboard for terminals too small for panels.
func (d *dashboard) compact(w int) []string {
	c := d.cur
	lines := []string{
		fmt.Sprintf(" CPU %3.0f%%  RAM %3.0f%%  load %.2f", d.cpu.Last(), c.MemUsedPct(), c.Load[0]),
	}
	if c.HasTemp {
		lines[0] += fmt.Sprintf("  %.0f°C", c.TempC)
	}
	for _, f := range c.Ifaces {
		if f.Default {
			state := "up"
			if !f.Up {
				state = "DOWN"
			}
			addr := ""
			if len(f.Addrs) > 0 {
				addr = f.Addrs[0]
			}
			lines = append(lines, fmt.Sprintf(" %s %s %s", f.Name, state, addr))
		}
	}
	if dev := d.graphDev(); dev != "" && d.haveRate {
		x := d.rates.Net[dev]
		lines = append(lines, fmt.Sprintf(" %s  ▼ %s  ▲ %s", dev, rate(x.RxBps, d.bits), rate(x.TxBps, d.bits)))
	}
	lines = append(lines, fmt.Sprintf(" %d clients", len(d.clients())))
	return lines
}

// ipLess orders clients by address (the order DHCP hands them out); entries
// without an address go last, by name.
func ipLess(a, b, na, nb string) bool {
	x, e1 := netip.ParseAddr(a)
	y, e2 := netip.ParseAddr(b)
	switch {
	case e1 == nil && e2 == nil:
		return x.Less(y)
	case e1 == nil:
		return true
	case e2 == nil:
		return false
	}
	return strings.ToLower(na) < strings.ToLower(nb)
}
