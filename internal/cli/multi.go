package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"text/tabwriter"

	"github.com/madushakv/owrt-tui/internal/compare"
	"github.com/madushakv/owrt-tui/internal/demo"
	"github.com/madushakv/owrt-tui/internal/profiles"
	"github.com/madushakv/owrt-tui/internal/session"
	"github.com/madushakv/owrt-tui/internal/transport"
	"github.com/madushakv/owrt-tui/internal/uci"
)

// applyProfile fills target, identity and port from the saved profile, without
// overriding anything given explicitly on the command line.
func (g *globals) applyProfile() error {
	st, err := profiles.Open()
	if err != nil {
		return err
	}
	p, err := st.Get(g.profile)
	if err != nil {
		return err
	}
	if g.target == "" || g.target == os.Getenv("OWRT_HOST") && os.Getenv("OWRT_HOST") != "" {
		g.target = p.Target
	}
	if g.identity == "" {
		g.identity = p.Identity
	}
	if g.port == "" {
		g.port = p.Port
	}
	return nil
}

func cmdProfiles(args []string, out io.Writer) error {
	st, err := profiles.Open()
	if err != nil {
		return err
	}
	if len(args) == 0 {
		ps, err := st.List()
		if err != nil {
			return err
		}
		if len(ps) == 0 {
			fmt.Fprintf(out, "no saved routers yet; add one with: owrt-tui profiles add <name> <user@host>\n(file: %s)\n", st.Path)
			return nil
		}
		tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "NAME\tTARGET\tPORT\tKEY\tNOTE")
		for _, p := range ps {
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", p.Name, p.Target, p.Port, p.Identity, p.Note)
		}
		return tw.Flush()
	}
	switch args[0] {
	case "add":
		fs := flag.NewFlagSet("profiles add", flag.ContinueOnError)
		id := fs.String("i", "", "ssh identity file")
		port := fs.String("p", "", "ssh port")
		note := fs.String("note", "", "a reminder of what this router is")
		// Accept flags after the two positional arguments too.
		var pos []string
		rest := args[1:]
		for len(rest) > 0 {
			if err := fs.Parse(rest); err != nil {
				return usageError(err.Error())
			}
			if fs.NArg() == 0 {
				break
			}
			pos = append(pos, fs.Arg(0))
			rest = fs.Args()[1:]
		}
		if len(pos) != 2 {
			return usageError("usage: owrt-tui profiles add <name> <target> [-i key] [-p port] [-note text]")
		}
		return st.Put(profiles.Profile{Name: pos[0], Target: pos[1], Identity: *id, Port: *port, Note: *note})
	case "rm", "remove", "delete":
		if len(args) != 2 {
			return usageError("usage: owrt-tui profiles rm <name>")
		}
		return st.Remove(args[1])
	}
	return usageError("usage: owrt-tui profiles [add <name> <target> ... | rm <name>]")
}

// open connects read-only to a router named by a profile, a target, or "demo".
func (g globals) openNamed(ctx context.Context, name string) (*session.Session, transport.Transport, error) {
	var t transport.Transport
	st, perr := profiles.Open()
	var p profiles.Profile
	var found bool
	if perr == nil {
		if pp, err := st.Get(name); err == nil {
			p, found = pp, true
		} else if !errors.Is(err, profiles.ErrNotFound) {
			return nil, nil, err
		}
	}
	switch {
	case found:
		var extra []string
		if p.Identity != "" {
			extra = append(extra, "-i", p.Identity)
		}
		if p.Port != "" {
			extra = append(extra, "-p", p.Port)
		}
		var err error
		if t, err = transport.NewSSH(p.Target, true, extra...); err != nil {
			return nil, nil, err
		}
	case name == "demo":
		t = demo.Router()
	default:
		o := g
		o.demo, o.target, o.identity, o.port = false, name, "", ""
		var err error
		if t, err = o.transport(); err != nil {
			return nil, nil, err
		}
	}
	t = transport.Retry{T: t, Attempts: 1}
	cctx, cancel := context.WithTimeout(ctx, g.timeout)
	defer cancel()
	s, err := session.Connect(cctx, t, true)
	if err != nil {
		t.Close()
		return nil, nil, fmt.Errorf("connecting to %s: %w", name, err)
	}
	return s, t, nil
}

func (g globals) loadConfigs(ctx context.Context, name string) ([]uci.Config, error) {
	s, t, err := g.openNamed(ctx, name)
	if err != nil {
		return nil, err
	}
	defer t.Close()
	cctx, cancel := context.WithTimeout(ctx, g.timeout)
	defer cancel()
	return s.LoadUCI(cctx)
}

func cmdCompare(g globals, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("compare", flag.ContinueOnError)
	cfg := fs.String("config", "", "only these config files, comma separated (e.g. network,firewall)")
	all := fs.Bool("all", false, "also show values that differ on any two devices (MAC addresses, radio paths)")
	if err := fs.Parse(args); err != nil {
		return usageError(err.Error())
	}
	names := fs.Args()
	switch len(names) {
	case 1:
		cur := g.profile
		switch {
		case g.demo:
			cur = "demo"
		case g.target != "":
			cur = g.target
		}
		if cur == "" {
			return usageError("compare with one argument needs -profile, -target or -demo for the first router")
		}
		names = []string{cur, names[0]}
	case 2:
	default:
		return usageError("usage: owrt-tui compare [-config a,b] [-all] [A] B")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	a, err := g.loadConfigs(ctx, names[0])
	if err != nil {
		return err
	}
	b, err := g.loadConfigs(ctx, names[1])
	if err != nil {
		return err
	}
	opt := compare.DefaultOptions()
	if *all {
		opt.IgnoreKeys = nil
	}
	if *cfg != "" {
		opt.Configs = strings.Split(*cfg, ",")
	}
	fmt.Fprint(out, compare.Format(compare.Compare(a, b, opt), names[0], names[1]))
	return nil
}
