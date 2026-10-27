// Package services manages init scripts: list, state, start/stop/restart and
// enable/disable. State comes from one round trip (init.d plus procd).
package services

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/madushakv/owrt-tui/internal/shq"
	"github.com/madushakv/owrt-tui/internal/transport"
)

// State is the runtime state as procd reports it.
type State int

const (
	Unknown State = iota // not registered with procd, or no instance
	Stopped
	Running
)

func (s State) String() string { return [...]string{"-", "stopped", "running"}[s] }

// Service is one /etc/init.d entry.
type Service struct {
	Name    string
	Enabled bool // has a rc.d start symlink
	Start   int  // START= priority from the script, 0 if absent
	State   State
}

// Action is something you can do to a service.
type Action string

const (
	Start   Action = "start"
	Stop    Action = "stop"
	Restart Action = "restart"
	Reload  Action = "reload"
	Enable  Action = "enable"
	Disable Action = "disable"
)

var valid = map[Action]bool{Start: true, Stop: true, Restart: true, Reload: true, Enable: true, Disable: true}

const listScript = `
for f in /etc/init.d/*; do
  [ -f "$f" ] && [ -x "$f" ] || continue
  n=${f##*/}
  e=0; "$f" enabled >/dev/null 2>&1 && e=1
  s=$(sed -n 's/^START=//p' "$f" | head -n1)
  echo "svc $n $e ${s:-0}"
done
echo '##ubus'
ubus call service list 2>/dev/null
`

// List returns every init script with its enabled flag and runtime state.
func List(ctx context.Context, t transport.Transport) ([]Service, error) {
	res, err := transport.RunScript(ctx, t, listScript)
	if err != nil {
		return nil, err
	}
	if res.ExitCode != 0 && strings.TrimSpace(res.Stdout) == "" {
		return nil, fmt.Errorf("listing services failed (exit %d): %s", res.ExitCode, strings.TrimSpace(res.Stderr))
	}
	return ParseList(res.Stdout), nil
}

// ParseList parses listScript output. Malformed lines are skipped.
func ParseList(out string) []Service {
	head, tail := out, ""
	if i := strings.Index(out, "##ubus"); i >= 0 {
		head, tail = out[:i], out[i+len("##ubus"):]
	}
	running := parseProcd(tail)

	var svcs []Service
	for _, l := range strings.Split(head, "\n") {
		f := strings.Fields(l)
		if len(f) < 3 || f[0] != "svc" {
			continue
		}
		s := Service{Name: f[1], Enabled: f[2] == "1"}
		if len(f) >= 4 {
			s.Start, _ = strconv.Atoi(f[3])
		}
		if st, ok := running[s.Name]; ok {
			s.State = st
		}
		svcs = append(svcs, s)
	}
	sort.Slice(svcs, func(i, j int) bool { return svcs[i].Name < svcs[j].Name })
	return svcs
}

// parseProcd reads "ubus call service list" JSON. A service is Running when any
// instance is running, Stopped when it has instances but none are running.
func parseProcd(js string) map[string]State {
	out := map[string]State{}
	var raw map[string]struct {
		Instances map[string]struct {
			Running bool `json:"running"`
		} `json:"instances"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(js)), &raw); err != nil {
		return out
	}
	for name, svc := range raw {
		if len(svc.Instances) == 0 {
			continue
		}
		st := Stopped
		for _, in := range svc.Instances {
			if in.Running {
				st = Running
				break
			}
		}
		out[name] = st
	}
	return out
}

// Command builds the shell command for an action.
func Command(name string, a Action) (string, error) {
	if !shq.Unit(name) {
		return "", fmt.Errorf("invalid service name %q", name)
	}
	if !valid[a] {
		return "", fmt.Errorf("unknown action %q", a)
	}
	// shq.Unit only admits [A-Za-z0-9._@-], so the name needs no quoting.
	return "/etc/init.d/" + name + " " + string(a), nil
}

// Warning returns a caution to show before the action, or "" when it is safe.
func Warning(name string, a Action) string {
	disruptive := a == Stop || a == Restart || a == Disable
	switch name {
	case "network":
		if disruptive {
			return "this restarts networking and can drop your SSH session"
		}
	case "dropbear":
		if disruptive {
			return "this is the SSH server; stopping or disabling it can lock you out"
		}
	case "firewall":
		if a == Stop || a == Disable {
			return "this removes firewall filtering"
		}
	case "uhttpd":
		if disruptive {
			return "this stops the LuCI web interface"
		}
	}
	return ""
}

// Do runs the action and returns its combined output.
func Do(ctx context.Context, t transport.Transport, name string, a Action) (string, error) {
	cmd, err := Command(name, a)
	if err != nil {
		return "", err
	}
	res, err := t.Run(ctx, cmd+" 2>&1", nil)
	if err != nil {
		return "", err
	}
	if res.ExitCode != 0 {
		return res.Stdout, fmt.Errorf("%s %s failed (exit %d): %s", name, a, res.ExitCode, strings.TrimSpace(res.Stdout))
	}
	return res.Stdout, nil
}
