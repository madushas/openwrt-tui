// Package session is the one entry point the UI talks to. It owns the
// transport, the probe result and the read-only switch, so a screen cannot
// forget to check whether writing is allowed: every mutating method goes
// through requireWrite.
package session

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/madushakv/owrt-tui/internal/changes"
	"github.com/madushakv/owrt-tui/internal/clients"
	"github.com/madushakv/owrt-tui/internal/device"
	"github.com/madushakv/owrt-tui/internal/health"
	"github.com/madushakv/owrt-tui/internal/metrics"
	"github.com/madushakv/owrt-tui/internal/pkgmgr"
	"github.com/madushakv/owrt-tui/internal/postcheck"
	"github.com/madushakv/owrt-tui/internal/services"
	"github.com/madushakv/owrt-tui/internal/transport"
	"github.com/madushakv/owrt-tui/internal/uci"
	"github.com/madushakv/owrt-tui/internal/validate"
	"github.com/madushakv/owrt-tui/internal/wificaps"
)

// ErrReadOnly is returned by every mutating method in read-only mode.
var ErrReadOnly = errors.New("read-only mode is on: change blocked")

// ErrNoPkgMgr means the router has neither apk nor opkg.
var ErrNoPkgMgr = errors.New("no package manager (apk or opkg) found on the device")

// Session is a connected, probed router.
type Session struct {
	T        transport.Transport
	Info     *device.Info
	ReadOnly bool
	pkg      *pkgmgr.Runner
}

// Connect probes the device and prepares the package manager backend.
func Connect(ctx context.Context, t transport.Transport, readOnly bool) (*Session, error) {
	info, err := device.Probe(ctx, t)
	if err != nil {
		return nil, err
	}
	s := &Session{T: t, Info: info, ReadOnly: readOnly}
	if b, err := pkgmgr.ForName(info.PkgMgr); err == nil {
		s.pkg = &pkgmgr.Runner{B: b, T: t}
	}
	return s, nil
}

// Host is the address or name the session connects to (without the user), for
// checks that need to know which interface carries the connection.
func (s *Session) Host() string {
	d, ok := s.T.(interface{ Describe() string })
	if !ok {
		return ""
	}
	h := d.Describe()
	if i := strings.LastIndex(h, "@"); i >= 0 {
		h = h[i+1:]
	}
	return h
}

func (s *Session) requireWrite() error {
	if s.ReadOnly {
		return ErrReadOnly
	}
	return nil
}

// ---- configuration ----

// LoadUCI reads every config file.
func (s *Session) LoadUCI(ctx context.Context) ([]uci.Config, error) {
	res, err := s.T.Run(ctx, "uci export", nil)
	if err != nil {
		return nil, err
	}
	if res.ExitCode != 0 {
		return nil, fmt.Errorf("uci export failed (exit %d): %s", res.ExitCode, strings.TrimSpace(res.Stderr))
	}
	return uci.ParseExport(res.Stdout)
}

// Apply writes the staged set to the router. With changes.WithRollback a
// watchdog on the router restores the old config unless Confirm is called in
// time. If the connection drops mid-apply (expected when network settings
// change) the error wraps transport.ErrConn and the caller should reconnect and
// check RollbackStatus.
func (s *Session) Apply(ctx context.Context, set *changes.Set, plan changes.Plan) (string, error) {
	if err := s.requireWrite(); err != nil {
		return "", err
	}
	script, err := set.Script(plan)
	if err != nil {
		return "", err
	}
	res, err := transport.RunScript(ctx, s.T, script)
	if err != nil {
		return "", err
	}
	if res.ExitCode != 0 {
		return res.Stdout, fmt.Errorf("apply failed (%s): %s", applyExitMeaning(res.ExitCode), strings.TrimSpace(res.Stderr))
	}
	return strings.TrimSpace(res.Stdout), nil
}

func applyExitMeaning(code int) string {
	switch code {
	case 3:
		return "could not snapshot /etc/config; nothing changed"
	case 4:
		return "staging failed; changes were reverted"
	case 5:
		return "uci commit failed"
	}
	return fmt.Sprintf("exit %d", code)
}

// Confirm cancels a pending rollback for the plan.
func (s *Session) Confirm(ctx context.Context, planID string) error {
	if err := s.requireWrite(); err != nil {
		return err
	}
	cmd, err := changes.ConfirmCommand(planID)
	if err != nil {
		return err
	}
	return s.runOK(ctx, cmd)
}

// RollbackStatus returns "pending", "confirmed" or "rolledback". Reading it is
// allowed in read-only mode.
func (s *Session) RollbackStatus(ctx context.Context, planID string) (string, error) {
	cmd, err := changes.StatusCommand(planID)
	if err != nil {
		return "", err
	}
	res, err := s.T.Run(ctx, cmd, nil)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(res.Stdout), nil
}

// Restore copies a plan's snapshot back (manual undo after an apply).
func (s *Session) Restore(ctx context.Context, planID string) error {
	if err := s.requireWrite(); err != nil {
		return err
	}
	cmd, err := changes.RestoreCommand(planID)
	if err != nil {
		return err
	}
	return s.runOK(ctx, cmd)
}

// ---- live statistics ----

// Sample reads live statistics in one round trip. It only reads, so it is
// allowed in read-only mode.
func (s *Session) Sample(ctx context.Context) (metrics.Sample, error) {
	res, err := transport.RunScript(ctx, s.T, metrics.Script)
	if err != nil {
		return metrics.Sample{}, err
	}
	if res.ExitCode != 0 && strings.TrimSpace(res.Stdout) == "" {
		return metrics.Sample{}, fmt.Errorf("sampling failed (exit %d): %s", res.ExitCode, strings.TrimSpace(res.Stderr))
	}
	return metrics.Parse(res.Stdout)
}

// PostCheck runs the read-only health checks used to decide whether a change
// can be kept. It is allowed in read-only mode.
func (s *Session) PostCheck(ctx context.Context) (postcheck.Result, error) {
	res, err := transport.RunScript(ctx, s.T, postcheck.Script)
	if err != nil {
		return nil, err
	}
	return postcheck.Parse(res.Stdout), nil
}

// HealthFacts gathers what the health rules need beyond the configuration. It
// only reads, so it is allowed in read-only mode. Missing pieces stay empty.
func (s *Session) HealthFacts(ctx context.Context) (health.Facts, error) {
	res, err := transport.RunScript(ctx, s.T, health.Script)
	if err != nil {
		return health.Facts{}, err
	}
	f := health.ParseFacts(res.Stdout)
	if svcs, err := s.Services(ctx); err == nil {
		f.Services = svcs
	}
	return f, nil
}

// Clients reads the raw per-device data. It only reads.
func (s *Session) Clients(ctx context.Context) (clients.Raw, error) {
	res, err := transport.RunScript(ctx, s.T, clients.Script)
	if err != nil {
		return clients.Raw{}, err
	}
	return clients.ParseRaw(res.Stdout), nil
}

var devRe = regexp.MustCompile(`^[A-Za-z0-9._-]{1,15}$`)

// Kick disconnects a Wi-Fi client; it may reconnect at once.
func (s *Session) Kick(ctx context.Context, iface, mac string) error {
	if err := s.requireWrite(); err != nil {
		return err
	}
	if !devRe.MatchString(iface) || validate.MAC(mac) != nil {
		return fmt.Errorf("invalid interface or MAC address")
	}
	return s.runOK(ctx, fmt.Sprintf(`ubus call hostapd.%s del_client '{"addr":"%s","reason":5,"deauth":false,"ban_time":0}'`, iface, strings.ToLower(mac)))
}

// Wake sends a wake-on-LAN packet through dev, using etherwake if installed.
func (s *Session) Wake(ctx context.Context, dev, mac string) error {
	if err := s.requireWrite(); err != nil {
		return err
	}
	if !devRe.MatchString(dev) || validate.MAC(mac) != nil {
		return fmt.Errorf("invalid interface or MAC address")
	}
	res, err := s.T.Run(ctx, fmt.Sprintf("command -v etherwake >/dev/null 2>&1 || exit 127; etherwake -i %s %s", dev, strings.ToLower(mac)), nil)
	if err != nil {
		return err
	}
	switch res.ExitCode {
	case 0:
		return nil
	case 127:
		return fmt.Errorf("wake-on-LAN needs the etherwake package on the router (install it on the Packages screen)")
	}
	return fmt.Errorf("etherwake failed (exit %d): %s", res.ExitCode, strings.TrimSpace(res.Stderr))
}

// WifiCaps reads what each radio supports. It only reads, so it is allowed in
// read-only mode; a router without iw simply reports no capabilities.
func (s *Session) WifiCaps(ctx context.Context) (wificaps.Caps, error) {
	res, err := transport.RunScript(ctx, s.T, wificaps.Script)
	if err != nil {
		return wificaps.Caps{}, err
	}
	return wificaps.Parse(res.Stdout), nil
}

// ---- services ----

// Services lists init scripts with state.
func (s *Session) Services(ctx context.Context) ([]services.Service, error) {
	return services.List(ctx, s.T)
}

// ServiceDo runs start, stop, restart, reload, enable or disable.
func (s *Session) ServiceDo(ctx context.Context, name string, a services.Action) (string, error) {
	if err := s.requireWrite(); err != nil {
		return "", err
	}
	return services.Do(ctx, s.T, name, a)
}

// ---- packages ----

func (s *Session) runner() (*pkgmgr.Runner, error) {
	if s.pkg == nil {
		return nil, ErrNoPkgMgr
	}
	return s.pkg, nil
}

// PkgManager returns "apk", "opkg" or "".
func (s *Session) PkgManager() string { return s.Info.PkgMgr }

// PkgUpdate refreshes package indexes.
func (s *Session) PkgUpdate(ctx context.Context) (string, error) {
	r, err := s.runner()
	if err != nil {
		return "", err
	}
	if err := s.requireWrite(); err != nil {
		return "", err
	}
	return r.Exec(ctx, r.B.CmdUpdate())
}

// PkgInstalled lists installed packages.
func (s *Session) PkgInstalled(ctx context.Context) ([]pkgmgr.Package, error) {
	r, err := s.runner()
	if err != nil {
		return nil, err
	}
	out, err := r.Exec(ctx, r.B.CmdListInstalled())
	if err != nil {
		return nil, err
	}
	return r.B.ParseList(out), nil
}

// PkgUpgradable lists packages with a newer version in the feeds.
func (s *Session) PkgUpgradable(ctx context.Context) ([]pkgmgr.Package, error) {
	r, err := s.runner()
	if err != nil {
		return nil, err
	}
	out, err := r.Exec(ctx, r.B.CmdListUpgradable())
	if err != nil {
		return nil, err
	}
	return r.B.ParseList(out), nil
}

// PkgSearch searches the feeds, ranking name matches above description matches.
func (s *Session) PkgSearch(ctx context.Context, query string) ([]pkgmgr.Package, error) {
	r, err := s.runner()
	if err != nil {
		return nil, err
	}
	cmd, err := r.B.CmdSearch(query)
	if err != nil {
		return nil, err
	}
	out, err := r.Exec(ctx, cmd)
	if err != nil {
		return nil, err
	}
	return pkgmgr.RankSearch(r.B.ParseSearch(out), query), nil
}

// PkgInfo returns the raw package information text.
func (s *Session) PkgInfo(ctx context.Context, name string) (string, error) {
	r, err := s.runner()
	if err != nil {
		return "", err
	}
	cmd, err := r.B.CmdInfo(name)
	if err != nil {
		return "", err
	}
	return r.Exec(ctx, cmd)
}

// PkgPlan is the dry-run result shown before the user confirms.
type PkgPlan struct {
	Actions []pkgmgr.Action
	Raw     string
	// NeedKB is not known without package sizes; FreeKB is the overlay space
	// left, so the UI can warn when it is nearly full.
	FreeKB int64
}

// PkgSimulate dry-runs an install (remove=false) or removal. Always call this
// and show the result before PkgInstall or PkgRemove. A simulation changes
// nothing, so it is allowed in read-only mode.
func (s *Session) PkgSimulate(ctx context.Context, name string, remove bool) (PkgPlan, error) {
	r, err := s.runner()
	if err != nil {
		return PkgPlan{}, err
	}
	var cmd string
	if remove {
		cmd, err = r.B.CmdDel(name, true)
	} else {
		cmd, err = r.B.CmdAdd(name, true)
	}
	if err != nil {
		return PkgPlan{}, err
	}
	out, err := r.Exec(ctx, cmd)
	plan := PkgPlan{Raw: out, FreeKB: s.Info.OverlayKB.Avail}
	if err != nil {
		return plan, err
	}
	plan.Actions = r.B.ParseActions(out)
	return plan, nil
}

// PkgInstall installs a package (with its dependencies).
func (s *Session) PkgInstall(ctx context.Context, name string) (string, error) {
	return s.pkgChange(ctx, name, false)
}

// PkgRemove removes a package.
func (s *Session) PkgRemove(ctx context.Context, name string) (string, error) {
	return s.pkgChange(ctx, name, true)
}

func (s *Session) pkgChange(ctx context.Context, name string, remove bool) (string, error) {
	r, err := s.runner()
	if err != nil {
		return "", err
	}
	if err := s.requireWrite(); err != nil {
		return "", err
	}
	var cmd string
	if remove {
		cmd, err = r.B.CmdDel(name, false)
	} else {
		cmd, err = r.B.CmdAdd(name, false)
	}
	if err != nil {
		return "", err
	}
	return r.Exec(ctx, cmd)
}

// ---- power ----

// Reboot restarts the router. The command is detached so ssh returns cleanly
// instead of reporting a dropped connection.
func (s *Session) Reboot(ctx context.Context) error {
	if err := s.requireWrite(); err != nil {
		return err
	}
	return s.runOK(ctx, "(sleep 1; reboot) </dev/null >/dev/null 2>&1 &")
}

// PowerOff halts the router; it cannot be turned back on remotely.
func (s *Session) PowerOff(ctx context.Context) error {
	if err := s.requireWrite(); err != nil {
		return err
	}
	return s.runOK(ctx, "(sleep 1; poweroff) </dev/null >/dev/null 2>&1 &")
}

func (s *Session) runOK(ctx context.Context, cmd string) error {
	res, err := s.T.Run(ctx, cmd, nil)
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("command failed (exit %d): %s", res.ExitCode, strings.TrimSpace(res.Stderr))
	}
	return nil
}
