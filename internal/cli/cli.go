// Package cli is the command-line front end. It always connects read-only, so
// it is safe to point at a real router for the first time.
package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/madushakv/owrt-tui/internal/demo"
	"github.com/madushakv/owrt-tui/internal/diag"
	"github.com/madushakv/owrt-tui/internal/logs"
	"github.com/madushakv/owrt-tui/internal/metrics"
	"github.com/madushakv/owrt-tui/internal/session"
	"github.com/madushakv/owrt-tui/internal/transport"
)

// Version is set at build time with -ldflags "-X .../internal/cli.Version=v1.2.3".
var Version = "dev"

// usageError makes Run exit with status 2 instead of 1.
type usageError string

func (e usageError) Error() string { return string(e) }

// TUI launches the terminal UI. main sets it, which keeps this package free of
// the Charm dependencies. Nil means no UI is available.
var TUI func(ctx context.Context, s *session.Session, timeout time.Duration) error

// TUIOpts is what the connecting UI needs. The UI finds or asks for the router
// itself when Target is empty, and helps fix connection problems.
type TUIOpts struct {
	Target, Identity, Port string
	Write                  bool
	ExportDir              string
	Timeout                time.Duration
}

// TUIConnect launches the terminal UI on its Connect screen. Nil falls back to
// connecting up front and calling TUI.
var TUIConnect func(ctx context.Context, o TUIOpts) error

type globals struct {
	target, identity, port string
	demo, write            bool
	exportDir              string
	timeout                time.Duration
	icons                  string
	profile                string
}

// env is what a subcommand gets.
type env struct {
	ctx     context.Context
	timeout time.Duration
	s       *session.Session
	out     io.Writer
}

// timed returns a context that ends after the global timeout.
func (e env) timed() (context.Context, context.CancelFunc) {
	return context.WithTimeout(e.ctx, e.timeout)
}

type command struct {
	name, args, help string
	run              func(env, []string) error
}

var commands []command

func init() {
	commands = []command{
		{"profiles", "[add <name> <target> [-i key] [-p port] [-note text] | rm <name>]", "list, add or remove saved routers (use with -profile)", nil},
		{"compare", "[-config a,b] [-all] [A] B", "show how two routers' configurations differ (A defaults to the current router; each is a profile, user@host, or demo)", nil},
		{"probe", "", "show what the router is and what it can do", cmdProbe},
		{"uci", "[-reveal] [config]", "show configuration (secrets masked unless -reveal)", cmdUCI},
		{"services", "", "list init scripts with enabled and running state", cmdServices},
		{"logs", "[-n 100] [-level warn] [-proc name] [-grep text] [-f]", "show the system log", cmdLogs},
		{"pkg", "installed|upgradable|search <q>|info <pkg>|plan-add <pkg>|plan-del <pkg>", "package manager (read-only; plans are dry runs)", cmdPkg},
		{"sample", "[-raw]", "one live reading (what the dashboard shows); -raw prints the router's unparsed output", cmdSample},
		{"ping", "[-c 4] <host>", "ping from the router", cmdPing},
		{"trace", "<host>", "traceroute from the router", cmdTrace},
		{"lookup", "<name> [dns-server]", "DNS lookup from the router", cmdLookup},
	}
}

// Run executes the program and returns the exit status.
func Run(args []string, out, errw io.Writer) int {
	var g globals
	fs := flag.NewFlagSet("owrt-tui", flag.ContinueOnError)
	fs.SetOutput(errw)
	fs.StringVar(&g.target, "target", os.Getenv("OWRT_HOST"), "router as ssh sees it: user@host or a Host alias from ~/.ssh/config (env OWRT_HOST)")
	fs.StringVar(&g.identity, "i", "", "ssh identity file")
	fs.StringVar(&g.port, "p", "", "ssh port")
	fs.StringVar(&g.exportDir, "export-dir", "", "tui only: git repository to record the masked configuration in after every apply")
	fs.BoolVar(&g.write, "write", false, "tui only: allow changes (default is read-only)")
	fs.BoolVar(&g.demo, "demo", false, "use a built-in fake router instead of connecting")
	fs.StringVar(&g.profile, "profile", "", "saved router name (see: owrt-tui profiles); -target, -i and -p override it")
	showVersion := fs.Bool("version", false, "print the version and exit")
	fs.StringVar(&g.icons, "icons", "", "tui only: auto, unicode or ascii (plain-ASCII symbols for the Linux console / PuTTY; env OWRT_TUI_ICONS)")
	fs.DurationVar(&g.timeout, "timeout", 30*time.Second, "per-command timeout")
	fs.Usage = func() { usage(errw) }
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if *showVersion {
		fmt.Fprintln(out, "owrt-tui", Version)
		return 0
	}
	if g.profile != "" {
		if err := g.applyProfile(); err != nil {
			return report(errw, err)
		}
	}
	rest := fs.Args()
	if len(rest) > 0 && rest[0] == "profiles" {
		return report(errw, cmdProfiles(rest[1:], out))
	}
	if len(rest) > 0 && rest[0] == "compare" {
		return report(errw, cmdCompare(g, rest[1:], out))
	}
	if (TUI != nil || TUIConnect != nil) && (len(rest) == 0 || rest[0] == "tui") {
		return runTUI(g, errw)
	}
	if len(rest) == 0 {
		usage(errw)
		return 2
	}
	var cmd *command
	for i := range commands {
		if commands[i].name == rest[0] {
			cmd = &commands[i]
		}
	}
	if cmd == nil {
		fmt.Fprintf(errw, "unknown command %q\n\n", rest[0])
		usage(errw)
		return 2
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	t, err := g.transport()
	if err != nil {
		return report(errw, err)
	}
	// Safe only because this front end is read-only: a dropped connection can be
	// retried once without any risk of applying a change twice.
	t = transport.Retry{T: t, Attempts: 1}
	defer t.Close()

	cctx, cancel := context.WithTimeout(ctx, g.timeout)
	s, err := session.Connect(cctx, t, true) // always read-only
	cancel()
	if err != nil {
		return report(errw, fmt.Errorf("connecting to %s: %w", t.Describe(), err))
	}
	return report(errw, cmd.run(env{ctx: ctx, timeout: g.timeout, s: s, out: out}, rest[1:]))
}

func runTUI(g globals, errw io.Writer) int {
	if g.icons != "" {
		os.Setenv("OWRT_TUI_ICONS", g.icons)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if !g.demo && TUIConnect != nil {
		return report(errw, TUIConnect(ctx, TUIOpts{Target: g.target, Identity: g.identity, Port: g.port, Write: g.write, ExportDir: g.exportDir, Timeout: g.timeout}))
	}
	if TUI == nil {
		return report(errw, usageError("the terminal UI is not available in this build"))
	}
	t, err := g.transport()
	if err != nil {
		return report(errw, err)
	}
	// No Retry wrapper here: with write access a dropped connection does not say
	// whether the command ran, so replaying it could apply a change twice.
	if !g.write {
		t = transport.Retry{T: t, Attempts: 1}
	}
	defer t.Close()
	cctx, cancel := context.WithTimeout(ctx, g.timeout)
	s, err := session.Connect(cctx, t, !g.write)
	cancel()
	if err != nil {
		return report(errw, fmt.Errorf("connecting to %s: %w", t.Describe(), err))
	}
	return report(errw, TUI(ctx, s, g.timeout))
}

func report(errw io.Writer, err error) int {
	if err == nil {
		return 0
	}
	fmt.Fprintln(errw, "error:", err)
	var u usageError
	if errors.As(err, &u) {
		return 2
	}
	return 1
}

func (g globals) transport() (transport.Transport, error) {
	if g.demo {
		return demo.Router(), nil
	}
	if g.target == "" {
		return nil, usageError("no router given: use -target user@host (or set OWRT_HOST), or -demo to try it without one")
	}
	var extra []string
	if g.identity != "" {
		extra = append(extra, "-i", g.identity)
	}
	if g.port != "" {
		extra = append(extra, "-p", g.port)
	}
	// batch=true: the program cannot show a password prompt, so use keys or an agent.
	return transport.NewSSH(g.target, true, extra...)
}

func usage(w io.Writer) {
	fmt.Fprintln(w, "owrt-tui: inspect an OpenWrt router over ssh (read-only)")
	fmt.Fprintln(w, "\nusage: owrt-tui [-target user@host] [-i key] [-p port] [-demo] [-write] [-export-dir dir] [-timeout 30s] [command] [args]\n       (no command, or tui, opens the terminal UI; it is read-only unless -write is given)")
	fmt.Fprintln(w, "\ncommands:")
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	for _, c := range commands {
		fmt.Fprintf(tw, "  %s\t%s\n", c.name, c.help)
		if c.args != "" {
			fmt.Fprintf(tw, "  \t  args: %s\n", c.args)
		}
	}
	tw.Flush()
}

// ---- commands ----

func cmdProbe(e env, _ []string) error {
	i := e.s.Info
	tw := tabwriter.NewWriter(e.out, 0, 0, 2, ' ', 0)
	row := func(k, v string) { fmt.Fprintf(tw, "%s\t%s\n", k, v) }
	row("Host", i.Hostname)
	row("Model", i.Model)
	row("Release", strings.TrimSpace(i.Distro+" "+i.Version+" "+i.Revision))
	row("Kernel", i.Kernel)
	row("Target", i.Target+" ("+i.Arch+")")
	row("Packages", orDash(i.PkgMgr))
	row("Firewall", orDash(i.Firewall))
	row("Legacy switch", yesNo(i.Swconfig))
	row("Overlay free", fmt.Sprintf("%.1f MiB of %.1f MiB", float64(i.OverlayKB.Avail)/1024, float64(i.OverlayKB.Total)/1024))
	row("Uptime", uptime(i.UptimeSecs))
	var tools []string
	for k, ok := range i.Tools {
		if ok {
			tools = append(tools, k)
		}
	}
	sort.Strings(tools)
	row("Tools", strings.Join(tools, " "))
	if err := tw.Flush(); err != nil {
		return err
	}
	for _, a := range i.Advisories() {
		fmt.Fprintf(e.out, "%s: %s\n", strings.ToUpper(a.Level), a.Text)
	}
	return nil
}

func cmdUCI(e env, args []string) error {
	fs := flag.NewFlagSet("uci", flag.ContinueOnError)
	reveal := fs.Bool("reveal", false, "show secret values")
	if err := fs.Parse(args); err != nil {
		return usageError(err.Error())
	}
	ctx, cancel := e.timed()
	defer cancel()
	cfgs, err := e.s.LoadUCI(ctx)
	if err != nil {
		return err
	}
	want := fs.Arg(0)
	found := false
	for _, c := range cfgs {
		if want != "" && c.Name != want {
			continue
		}
		found = true
		fmt.Fprintf(e.out, "# %s\n", c.Name)
		for _, s := range c.Sections {
			fmt.Fprintf(e.out, "%s.%s  (%s)\n", c.Name, s.Ref(), s.Type)
			for _, o := range s.Options {
				kind := "option"
				if o.IsList {
					kind = "list"
				}
				fmt.Fprintf(e.out, "    %s %s = %s\n", kind, o.Key, o.Display(*reveal))
			}
		}
		fmt.Fprintln(e.out)
	}
	if !found {
		return fmt.Errorf("no config named %q", want)
	}
	return nil
}

func cmdServices(e env, _ []string) error {
	ctx, cancel := e.timed()
	defer cancel()
	svcs, err := e.s.Services(ctx)
	if err != nil {
		return err
	}
	tw := tabwriter.NewWriter(e.out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tENABLED\tSTATE\tSTART")
	for _, s := range svcs {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%d\n", s.Name, yesNo(s.Enabled), s.State, s.Start)
	}
	return tw.Flush()
}

func cmdLogs(e env, args []string) error {
	fs := flag.NewFlagSet("logs", flag.ContinueOnError)
	n := fs.Int("n", 100, "number of lines")
	level := fs.String("level", "", "show this severity and worse (err, warn, notice, info, debug)")
	proc := fs.String("proc", "", "only this process (substring)")
	grep := fs.String("grep", "", "only lines containing this text")
	follow := fs.Bool("f", false, "keep following new lines until interrupted")
	if err := fs.Parse(args); err != nil {
		return usageError(err.Error())
	}
	if *level != "" && !logs.ValidSeverity(*level) {
		return usageError(fmt.Sprintf("unknown level %q", *level))
	}
	f := logs.Filter{MinSeverity: *level, Proc: *proc, Contains: *grep}
	if *follow {
		err := logs.Follow(e.ctx, e.s.T, *n, func(en logs.Entry) {
			if f.Match(en) {
				fmt.Fprintln(e.out, en.Raw)
			}
		})
		if errors.Is(err, context.Canceled) {
			return nil // Ctrl-C is how you leave follow mode
		}
		return err
	}
	ctx, cancel := e.timed()
	defer cancel()
	entries, err := logs.Fetch(ctx, e.s.T, *n)
	if err != nil {
		return err
	}
	for _, en := range f.Apply(entries) {
		fmt.Fprintln(e.out, en.Raw)
	}
	return nil
}

func cmdPkg(e env, args []string) error {
	if len(args) == 0 {
		return usageError("pkg needs a subcommand: installed, upgradable, search, info, plan-add, plan-del")
	}
	ctx, cancel := e.timed()
	defer cancel()
	arg := func() (string, error) {
		if len(args) < 2 {
			return "", usageError("pkg " + args[0] + " needs an argument")
		}
		return args[1], nil
	}
	fmt.Fprintf(e.out, "package manager: %s\n", orDash(e.s.PkgManager()))
	switch args[0] {
	case "installed", "upgradable":
		list := e.s.PkgInstalled
		if args[0] == "upgradable" {
			list = e.s.PkgUpgradable
		}
		pk, err := list(ctx)
		if err != nil {
			return err
		}
		tw := tabwriter.NewWriter(e.out, 0, 0, 2, ' ', 0)
		for _, p := range pk {
			fmt.Fprintf(tw, "%s\t%s\t%s\n", p.Name, p.Version, p.Flags)
		}
		return tw.Flush()
	case "search":
		q, err := arg()
		if err != nil {
			return err
		}
		pk, err := e.s.PkgSearch(ctx, q)
		if err != nil {
			return err
		}
		tw := tabwriter.NewWriter(e.out, 0, 0, 2, ' ', 0)
		for _, p := range pk {
			fmt.Fprintf(tw, "%s\t%s\t%s\n", p.Name, p.Version, p.Desc)
		}
		return tw.Flush()
	case "info":
		p, err := arg()
		if err != nil {
			return err
		}
		out, err := e.s.PkgInfo(ctx, p)
		fmt.Fprint(e.out, out)
		return err
	case "plan-add", "plan-del":
		p, err := arg()
		if err != nil {
			return err
		}
		plan, err := e.s.PkgSimulate(ctx, p, args[0] == "plan-del")
		if err != nil {
			fmt.Fprint(e.out, plan.Raw)
			return err
		}
		for _, a := range plan.Actions {
			fmt.Fprintf(e.out, "%-12s %s %s\n", a.Verb, a.Name, a.Version)
		}
		fmt.Fprintf(e.out, "free flash: %.1f MiB (dry run, nothing was changed)\n", float64(plan.FreeKB)/1024)
		return nil
	}
	return usageError("unknown pkg subcommand " + args[0])
}

func cmdPing(e env, args []string) error {
	fs := flag.NewFlagSet("ping", flag.ContinueOnError)
	count := fs.Int("c", 4, "packets to send")
	if err := fs.Parse(args); err != nil {
		return usageError(err.Error())
	}
	if fs.NArg() != 1 {
		return usageError("ping needs exactly one host")
	}
	ctx, cancel := e.timed()
	defer cancel()
	r, err := diag.Ping(ctx, e.s.T, fs.Arg(0), *count)
	if err != nil {
		return err
	}
	fmt.Fprintf(e.out, "%s: %d sent, %d received, %d%% loss", r.Host, r.Sent, r.Received, r.LossPct)
	if r.Received > 0 {
		fmt.Fprintf(e.out, ", rtt min/avg/max %.1f/%.1f/%.1f ms", r.Min, r.Avg, r.Max)
	}
	fmt.Fprintln(e.out)
	return nil
}

func cmdTrace(e env, args []string) error {
	if len(args) != 1 {
		return usageError("trace needs exactly one host")
	}
	ctx, cancel := e.timed()
	defer cancel()
	return diag.Traceroute(ctx, e.s.T, args[0], func(l string) { fmt.Fprintln(e.out, l) })
}

func cmdLookup(e env, args []string) error {
	if len(args) < 1 || len(args) > 2 {
		return usageError("lookup needs a name and optionally a DNS server")
	}
	server := ""
	if len(args) == 2 {
		server = args[1]
	}
	ctx, cancel := e.timed()
	defer cancel()
	out, err := diag.Nslookup(ctx, e.s.T, args[0], server)
	fmt.Fprint(e.out, out)
	return err
}

// ---- helpers ----

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

func uptime(secs int) string {
	d, h, m := secs/86400, secs%86400/3600, secs%3600/60
	switch {
	case d > 0:
		return fmt.Sprintf("%dd %dh %dm", d, h, m)
	case h > 0:
		return fmt.Sprintf("%dh %dm", h, m)
	}
	return fmt.Sprintf("%dm", m)
}

// cmdSample prints one live reading. With -raw it prints exactly what the
// router returned, which is what to send along when a dashboard panel is empty
// or wrong on a particular device.
func cmdSample(e env, args []string) error {
	fs := flag.NewFlagSet("sample", flag.ContinueOnError)
	raw := fs.Bool("raw", false, "print the router's unparsed output")
	if err := fs.Parse(args); err != nil {
		return usageError(err.Error())
	}
	ctx, cancel := e.timed()
	defer cancel()
	if *raw {
		res, err := transport.RunScript(ctx, e.s.T, metrics.Script)
		if err != nil {
			return err
		}
		fmt.Fprint(e.out, res.Stdout)
		if res.Stderr != "" {
			fmt.Fprintln(e.out, "\n[stderr]\n"+res.Stderr)
		}
		return nil
	}
	s, err := e.s.Sample(ctx)
	if err != nil {
		return err
	}
	tw := tabwriter.NewWriter(e.out, 0, 0, 2, ' ', 0)
	row := func(k, v string) { fmt.Fprintf(tw, "%s\t%s\n", k, v) }
	row("Load", fmt.Sprintf("%.2f %.2f %.2f (%d cores)", s.Load[0], s.Load[1], s.Load[2], s.Cores))
	row("Memory", fmt.Sprintf("%.1f MiB available of %.1f MiB", float64(s.MemAvail)/1024, float64(s.MemTotal)/1024))
	if s.HasTemp {
		row("Temperature", fmt.Sprintf("%.0f C", s.TempC))
	} else {
		row("Temperature", "-  (no sensor found)")
	}
	row("Conntrack", fmt.Sprintf("%d of %d", s.CtCount, s.CtMax))
	var names []string
	for _, n := range s.Net {
		names = append(names, n.Name)
	}
	row("Net devices", strings.Join(names, " "))
	var ifs []string
	for _, i := range s.Ifaces {
		state := "down"
		if i.Up {
			state = "up"
		}
		ifs = append(ifs, i.Name+"("+state+")")
	}
	row("Interfaces", strings.Join(ifs, " "))
	row("DHCP leases", fmt.Sprint(len(s.Leases)))
	row("Wi-Fi clients", fmt.Sprint(len(s.Stations)))
	return tw.Flush()
}
