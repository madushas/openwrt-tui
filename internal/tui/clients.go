package tui

import (
	"context"
	"fmt"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/madushakv/owrt-tui/internal/changes"
	"github.com/madushakv/owrt-tui/internal/clients"
	"github.com/madushakv/owrt-tui/internal/traffic"
	"github.com/madushakv/owrt-tui/internal/uci"
)

type clientsScreen struct {
	e       *env
	list    []clients.Client
	cfgs    []uci.Config
	cur     int
	loading bool
	err     error
	form    *form
	formFor string // "name" or "route"

	prevS, nowS traffic.Sample // last two connection-tracking samples
	rates       map[string]traffic.Rate
	sortTraffic bool
}

func newClients(e *env) *clientsScreen { return &clientsScreen{e: e, loading: true} }

func (c *clientsScreen) Title() string   { return "Clients" }
func (c *clientsScreen) Capturing() bool { return c.form != nil }
func (c *clientsScreen) Keys() string {
	if c.form != nil {
		return "enter edit  s stage  esc cancel"
	}
	return "n name  s make static  b block/unblock  v route via VPN/WAN  k kick  w wake  t sort by traffic  g reload"
}

type clientsMsg struct {
	list []clients.Client
	cfgs []uci.Config
	tr   traffic.Sample
	err  error
}

func (c *clientsScreen) Init() tea.Cmd { return c.load() }

func (c *clientsScreen) load() tea.Cmd {
	return c.e.call(func(ctx context.Context) tea.Msg {
		cfgs, err := c.e.loadUCI(ctx)
		if err != nil {
			return clientsMsg{err: err}
		}
		raw, err := c.e.S.Clients(ctx)
		if err != nil {
			return clientsMsg{err: err}
		}
		tr, _ := c.e.S.Traffic(ctx) // best effort; the columns just stay empty
		return clientsMsg{list: clients.Merge(raw, cfgs), cfgs: cfgs, tr: tr}
	})
}

type clientActionMsg struct {
	what string
	err  error
}

func (c *clientsScreen) Update(msg tea.Msg) (screen, tea.Cmd) {
	switch m := msg.(type) {
	case clientsMsg:
		c.loading, c.err = false, m.err
		if m.err == nil {
			c.list, c.cfgs = m.list, m.cfgs
			c.prevS, c.nowS = c.nowS, m.tr
			c.rates = traffic.Rates(c.prevS, c.nowS)
			c.sortList()
			c.cur = clampInt(c.cur, 0, len(c.list)-1)
		}
	case uciChangedMsg:
		return c, c.load()
	case clientActionMsg:
		if m.err != nil {
			return c, toastErr(m.err)
		}
		return c, toast(m.what)
	case tea.KeyPressMsg:
		if c.form != nil {
			return c.formKey(m)
		}
		return c.key(m.String())
	}
	return c, nil
}

// weight returns the traffic figure used for sorting: the live rate when two
// samples exist, otherwise the bytes of open connections.
func (c *clientsScreen) weight(ip string) float64 {
	if r, ok := c.rates[ip]; ok {
		return r.Up + r.Down
	}
	u := c.nowS.By[ip]
	return float64(u.Up + u.Down)
}

func (c *clientsScreen) sortList() {
	if !c.sortTraffic {
		return
	}
	sort.SliceStable(c.list, func(i, j int) bool { return c.weight(c.list[i].IP) > c.weight(c.list[j].IP) })
}

// trafficCell is the TRAFFIC column for one client.
func (c *clientsScreen) trafficCell(ip string) string {
	if !c.nowS.Available || ip == "" {
		return ""
	}
	u, ok := c.nowS.By[ip]
	if !ok {
		return ""
	}
	if !c.nowS.Acct {
		return fmt.Sprintf("%d conn", u.Conns)
	}
	if r, ok := c.rates[ip]; ok {
		return "↓" + human(int64(r.Down)) + "/s ↑" + human(int64(r.Up)) + "/s"
	}
	return "↓" + human(u.Down) + " ↑" + human(u.Up)
}

func (c *clientsScreen) selected() (clients.Client, bool) {
	if len(c.list) == 0 {
		return clients.Client{}, false
	}
	return c.list[c.cur], true
}

func (c *clientsScreen) stage(list []changes.Change, err error, what string) tea.Cmd {
	if err != nil {
		return toastErr(err)
	}
	if len(list) == 0 {
		return toast("nothing to change")
	}
	for _, ch := range list {
		if err := c.e.Staged.Add(ch); err != nil {
			return toastErr(err)
		}
	}
	return toast(what + " staged; review and apply on the Changes screen")
}

func (c *clientsScreen) key(k string) (screen, tea.Cmd) {
	switch k {
	case "up", "k":
		if k == "k" {
			break // "k" is kick; arrows and j/down move
		}
		c.cur = clampInt(c.cur-1, 0, len(c.list)-1)
		return c, nil
	case "down", "j":
		c.cur = clampInt(c.cur+1, 0, len(c.list)-1)
		return c, nil
	case "t":
		c.sortTraffic = !c.sortTraffic
		c.sortList()
		c.cur = 0
		return c, nil
	case "g":
		c.loading = true
		c.e.invalidate()
		return c, c.load()
	}
	cl, ok := c.selected()
	if !ok {
		return c, nil
	}
	write := func() tea.Cmd {
		if c.e.S.ReadOnly {
			return toast("read-only mode: start with -write to change clients")
		}
		return nil
	}
	switch k {
	case "n":
		if t := write(); t != nil {
			return c, t
		}
		c.formFor = "name"
		c.form = newForm("Name for "+cl.MAC, &field{Label: "Name", Key: "name", Value: cl.Name, Required: true, Hint: "letters, digits, '.', '-', '_'"})
	case "s":
		if t := write(); t != nil {
			return c, t
		}
		ch, err := clients.MakeStatic(cl)
		return c, c.stage(ch, err, "reserved address")
	case "b":
		if t := write(); t != nil {
			return c, t
		}
		if cl.Blocked {
			return c, c.stage(clients.SetBlocked(cl, false), nil, "unblock")
		}
		return c, func() tea.Msg {
			return askMsg{Prompt: fmt.Sprintf("Block %s (%s)? Its traffic stops being forwarded; it can still reach the router.", cl.Display(), cl.MAC), Danger: false, Yes: func() tea.Cmd {
				return c.stage(clients.SetBlocked(cl, true), nil, "block")
			}}
		}
	case "v":
		if t := write(); t != nil {
			return c, t
		}
		if !clients.HasPBR(c.cfgs) {
			return c, toast("routing by client needs the pbr package and its config (see Packages)")
		}
		choices := append([]string{"(default route)"}, clients.RouteChoices(c.cfgs)...)
		cur := "(default route)"
		if cl.Route != "" {
			cur = cl.Route
		}
		c.formFor = "route"
		c.form = newForm("Route "+cl.Display()+" via", &field{Label: "Interface", Key: "iface", Kind: kChoice, Choices: choices, Value: cur})
	case "k":
		if t := write(); t != nil {
			return c, t
		}
		if cl.WifiIface == "" {
			return c, toast("only Wi-Fi clients can be kicked")
		}
		return c, func() tea.Msg {
			return askMsg{Prompt: fmt.Sprintf("Disconnect %s (%s) from Wi-Fi? It may reconnect right away.", cl.Display(), cl.MAC), Yes: func() tea.Cmd {
				return c.e.call(func(ctx context.Context) tea.Msg {
					return clientActionMsg{"disconnected " + cl.MAC, c.e.S.Kick(ctx, cl.WifiIface, cl.MAC)}
				})
			}}
		}
	case "w":
		if t := write(); t != nil {
			return c, t
		}
		if cl.Dev == "" {
			return c, toast("no network device known for this client")
		}
		return c, c.e.call(func(ctx context.Context) tea.Msg {
			return clientActionMsg{"wake-on-LAN sent to " + cl.MAC, c.e.S.Wake(ctx, cl.Dev, cl.MAC)}
		})
	}
	return c, nil
}

func (c *clientsScreen) formKey(m tea.KeyPressMsg) (screen, tea.Cmd) {
	cmd := c.form.Update(m)
	switch {
	case c.form.cancelled:
		c.form = nil
	case c.form.done:
		f, what := c.form, c.formFor
		c.form = nil
		cl, _ := c.selected()
		v := f.fields[0].Value
		if what == "name" {
			ch, err := clients.Rename(cl, v)
			return c, c.stage(ch, err, "name")
		}
		if v == "(default route)" {
			v = ""
		}
		ch, err := clients.SetRoute(cl, v)
		return c, c.stage(ch, err, "routing")
	}
	return c, cmd
}

func human(n int64) string {
	f := float64(n)
	for _, u := range []string{"B", "KB", "MB", "GB"} {
		if f < 1024 {
			return fmt.Sprintf("%.0f %s", f, u)
		}
		f /= 1024
	}
	return fmt.Sprintf("%.1f TB", f/1)
}

func (c *clientsScreen) View(w, h int) string {
	if c.form != nil {
		return c.form.View(w, h)
	}
	if c.loading && len(c.list) == 0 {
		return "\n  loading clients..."
	}
	if c.err != nil {
		return "\n  " + sBad.Render("error: "+c.err.Error())
	}
	if len(c.list) == 0 {
		return "\n  no clients found"
	}
	var b strings.Builder
	// Columns appear as the terminal allows: name, address and link always;
	// then traffic, then flags, then the MAC address.
	avail := w - 51
	showTraffic := avail >= 25
	if showTraffic {
		avail -= 25
	}
	showFlags := avail >= 14
	if showFlags {
		avail -= 14
	}
	showMAC := avail >= 18
	row := func(dot, name, ip, mac, link, tr, fl string) string {
		r := fmt.Sprintf("  %-3s %-18s %-15s ", dot, clip(name, 18), ip)
		if showMAC {
			r += fmt.Sprintf("%-17s ", mac)
		}
		r += fmt.Sprintf("%-9s", link)
		if showTraffic {
			r += fmt.Sprintf(" %-24s", clip(tr, 24))
		}
		if showFlags {
			r += " " + fl
		}
		return r
	}
	b.WriteString(sDim.Render(row("", "NAME", "ADDRESS", "MAC", "LINK", "TRAFFIC", "FLAGS")) + "\n")
	foot := ""
	switch {
	case !c.nowS.Available:
	case !c.nowS.Acct:
		foot = "traffic: byte counting is off; on the router run: " + traffic.EnableHint
	case len(c.rates) == 0:
		foot = "traffic: bytes of open connections; press g again for live rates"
	}
	if foot != "" {
		h--
	}
	lo, hi := window(len(c.list), c.cur, h-1)
	for i := lo; i < hi; i++ {
		cl := c.list[i]
		dot := "○"
		if cl.Online {
			dot = "●"
		} else if cl.Idle {
			dot = "◌"
		}
		if cl.Blocked && !showFlags {
			dot = "x"
		}
		link := "wired"
		if cl.WifiIface != "" {
			link = fmt.Sprintf("%d dBm", cl.Signal)
		}
		var fl []string
		if cl.Static {
			fl = append(fl, "static")
		}
		if cl.Blocked {
			fl = append(fl, "BLOCKED")
		}
		if cl.Route != "" {
			fl = append(fl, "via "+cl.Route)
		}
		if cl.WifiIface != "" {
			fl = append(fl, "↓"+human(cl.RxBytes)+" ↑"+human(cl.TxBytes))
		}
		line := row(dot, cl.Display(), cl.IP, cl.MAC, link, c.trafficCell(cl.IP), strings.Join(fl, "  "))
		if i == c.cur {
			line = sSel.Render(clip(line, w))
		} else {
			line = clip(line, w)
		}
		b.WriteString(line + "\n")
	}
	if foot != "" {
		b.WriteString(sDim.Render("  "+clip(foot, w-4)) + "\n")
	}
	return b.String()
}

// Detail describes the selected client, so the list can stay narrow.
func (c *clientsScreen) Detail(w, h int) string {
	cl, ok := c.selected()
	if !ok {
		return ""
	}
	var b strings.Builder
	line := func(k, v string) {
		if v != "" {
			b.WriteString(" " + sDim.Render(fmt.Sprintf("%-9s", k)) + clip(v, w-12) + "\n")
		}
	}
	b.WriteString(" " + sBold.Render(clip(cl.Display(), w-2)) + "\n")
	switch {
	case cl.Blocked:
		b.WriteString(" " + chipBad.Render("BLOCKED") + "\n\n")
	case cl.Online:
		b.WriteString(" " + chipGood.Render("ONLINE") + "\n\n")
	case cl.Idle:
		b.WriteString(" " + chipDim.Render("IDLE") + "\n\n")
	default:
		b.WriteString(" " + chipDim.Render("OFFLINE") + "\n\n")
	}
	line("Address", cl.IP)
	line("MAC", cl.MAC)
	if cl.WifiIface != "" {
		line("Wi-Fi", fmt.Sprintf("%s, %d dBm", cl.WifiIface, cl.Signal))
		if cl.Connected > 0 {
			line("Connected", fmt.Sprintf("%dm", cl.Connected/60))
		}
		line("Wi-Fi data", "↓"+human(cl.RxBytes)+" ↑"+human(cl.TxBytes))
	} else if cl.Online || cl.Idle {
		line("Link", "wired")
	}
	if cl.Static {
		line("Lease", "reserved (static)")
	}
	if cl.Route != "" {
		line("Route", "via "+cl.Route)
	}
	if t := c.trafficCell(cl.IP); t != "" {
		line("Traffic", t)
		if u, ok := c.nowS.By[cl.IP]; ok {
			line("Conns", fmt.Sprintf("%d open", u.Conns))
		}
	}
	return b.String()
}
