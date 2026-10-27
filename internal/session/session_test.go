package session

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/madushakv/owrt-tui/internal/changes"
	"github.com/madushakv/owrt-tui/internal/services"
	"github.com/madushakv/owrt-tui/internal/transport"
)

const probeOut = `##board
{"kernel":"6.12.5","hostname":"gw-test","model":"Test Board","board_name":"test","rootfs_type":"squashfs","release":{"distribution":"OpenWrt","version":"25.12.5","revision":"r1","target":"x86/64"}}
##uname
x86_64
##tools
apk
nft
fw4
ubus
uci
##swconfig
##df
Filesystem           1K-blocks      Used Available Use% Mounted on
/dev/sda2                 1000       100       900  10% /overlay
tmpfs                     2000        10      1990   1% /tmp
##uptime
100.00 90.00
`

const uciOut = `package system

config system
	option hostname 'gw-test'

package network

config interface 'lan'
	option proto 'static'
	option ipaddr '192.168.1.1'
`

const simOut = `(1/2) Installing libfoo (1.0-r1)
(2/2) Installing foo (2.0-r1)
OK: 47 MiB in 290 packages
`

func newFake() *transport.Fake {
	return (&transport.Fake{Name: "root@test"}).
		On("ubus call system board", probeOut).
		On("uci export", uciOut).
		On("ubus call service list", "svc dnsmasq 1 19\n##ubus\n{}").
		On("apk add --simulate", simOut).
		On("apk del --simulate", "(1/1) Purging foo (2.0-r1)\n").
		On("apk add -- ", "installed\n").
		On("apk update", "OK\n").
		On("reboot", "").
		On("touch", "confirmed\n").
		On(".rolledback", "pending\n")
}

func connect(t *testing.T, f *transport.Fake, ro bool) *Session {
	t.Helper()
	s, err := Connect(context.Background(), f, ro)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	return s
}

func stagedSet(t *testing.T) *changes.Set {
	t.Helper()
	set := &changes.Set{}
	if err := set.Add(changes.Change{Op: changes.OpSet, Config: "dhcp", Section: "lan", Option: "start", Value: "50", Old: "100"}); err != nil {
		t.Fatal(err)
	}
	return set
}

func TestConnectProbes(t *testing.T) {
	s := connect(t, newFake(), false)
	if s.Info.Hostname != "gw-test" || s.PkgManager() != "apk" || s.Info.Firewall != "fw4" {
		t.Errorf("info = %+v", s.Info)
	}
	if s.Info.OverlayKB.Avail != 900 {
		t.Errorf("overlay = %+v", s.Info.OverlayKB)
	}
}

func TestConnectFailsOnNonOpenWrt(t *testing.T) {
	f := (&transport.Fake{}).On("ubus call system board", "")
	if _, err := Connect(context.Background(), f, false); err == nil {
		t.Fatal("expected an error when no board info comes back")
	}
}

// Every mutating method must refuse in read-only mode and must not send a
// single command to the router. This is the property the whole mode rests on.
func TestReadOnlyBlocksEveryWriteAndSendsNothing(t *testing.T) {
	f := newFake()
	s := connect(t, f, true)
	before := len(f.Calls)
	ctx := context.Background()

	_, applyErr := s.Apply(ctx, stagedSet(t), changes.NewPlan(changes.CommitOnly, 0))
	_, svcErr := s.ServiceDo(ctx, "dnsmasq", services.Restart)
	_, updErr := s.PkgUpdate(ctx)
	_, insErr := s.PkgInstall(ctx, "foo")
	_, remErr := s.PkgRemove(ctx, "foo")

	writes := map[string]error{
		"Apply":      applyErr,
		"Confirm":    s.Confirm(ctx, "chg-20261025-091300"),
		"Restore":    s.Restore(ctx, "chg-20261025-091300"),
		"ServiceDo":  svcErr,
		"PkgUpdate":  updErr,
		"PkgInstall": insErr,
		"PkgRemove":  remErr,
		"Reboot":     s.Reboot(ctx),
		"PowerOff":   s.PowerOff(ctx),
	}
	for name, err := range writes {
		if !errors.Is(err, ErrReadOnly) {
			t.Errorf("%s: want ErrReadOnly, got %v", name, err)
		}
	}
	if after := len(f.Calls); after != before {
		t.Errorf("read-only mode sent %d command(s) to the router: %v", after-before, f.Calls[before:])
	}
}

func TestReadOnlyStillAllowsReadsAndSimulation(t *testing.T) {
	s := connect(t, newFake(), true)
	ctx := context.Background()

	cfgs, err := s.LoadUCI(ctx)
	if err != nil || len(cfgs) != 2 {
		t.Fatalf("LoadUCI: %d configs, %v", len(cfgs), err)
	}
	if svcs, err := s.Services(ctx); err != nil || len(svcs) != 1 {
		t.Fatalf("Services: %v %v", svcs, err)
	}
	plan, err := s.PkgSimulate(ctx, "foo", false)
	if err != nil || len(plan.Actions) != 2 || plan.FreeKB != 900 {
		t.Fatalf("PkgSimulate: %+v %v", plan, err)
	}
	if st, err := s.RollbackStatus(ctx, "chg-20261025-091300"); err != nil || st != "pending" {
		t.Fatalf("RollbackStatus: %q %v", st, err)
	}
}

func TestPkgSimulateThenInstall(t *testing.T) {
	f := newFake()
	s := connect(t, f, false)
	ctx := context.Background()

	plan, err := s.PkgSimulate(ctx, "foo", false)
	if err != nil || plan.Actions[1].Name != "foo" || plan.Actions[1].Verb != "Installing" {
		t.Fatalf("%+v %v", plan, err)
	}
	if len(f.CallsMatching("apk add -- ")) != 0 {
		t.Fatal("simulation must not install anything")
	}
	if _, err := s.PkgInstall(ctx, "foo"); err != nil {
		t.Fatal(err)
	}
	if len(f.CallsMatching("apk add -- ")) != 1 {
		t.Fatal("expected exactly one real install")
	}
	if _, err := s.PkgInstall(ctx, "foo; reboot"); err == nil {
		t.Fatal("injection in a package name must be rejected")
	}
}

func TestApplyScriptAndErrorMapping(t *testing.T) {
	f := newFake().OnFunc("uci set", func(cmd, stdin string) transport.Result {
		return transport.Result{Stdout: "owrt-tui: applied\n"}
	})
	s := connect(t, f, false)
	set := stagedSet(t)
	out, err := s.Apply(context.Background(), set, changes.NewPlan(changes.CommitOnly, 0))
	if err != nil || out != "owrt-tui: applied" {
		t.Fatalf("%q %v", out, err)
	}
	script := f.CallsMatching("uci commit")
	if len(script) != 1 || !strings.Contains(script[0], "uci set 'dhcp.lan.start=50'") {
		t.Fatalf("script = %v", script)
	}

	f2 := newFake().OnResult("uci set", transport.Result{ExitCode: 4, Stderr: "owrt-tui: staging failed"})
	s2 := connect(t, f2, false)
	_, err = s2.Apply(context.Background(), set, changes.NewPlan(changes.CommitOnly, 0))
	if err == nil || !strings.Contains(err.Error(), "reverted") {
		t.Fatalf("exit 4 should say changes were reverted, got %v", err)
	}
}

func TestApplyConnectionDropIsDistinguishable(t *testing.T) {
	f := newFake().OnFunc("uci set", func(cmd, stdin string) transport.Result { return transport.Result{} })
	s := connect(t, f, false)
	f.Fail = transport.ErrConn // the link goes down mid-apply, as it does when the LAN IP changes
	_, err := s.Apply(context.Background(), stagedSet(t), changes.NewPlan(changes.WithRollback, 60))
	if !errors.Is(err, transport.ErrConn) {
		t.Fatalf("want ErrConn so the UI can reconnect and check the rollback, got %v", err)
	}
}

func TestRebootIsDetached(t *testing.T) {
	f := newFake()
	s := connect(t, f, false)
	if err := s.Reboot(context.Background()); err != nil {
		t.Fatal(err)
	}
	c := f.CallsMatching("reboot")
	if len(c) != 1 || !strings.Contains(c[0], "(sleep 1; reboot)") || !strings.HasSuffix(c[0], "&") {
		t.Fatalf("reboot must be backgrounded so ssh exits cleanly: %v", c)
	}
}

func TestNoPackageManager(t *testing.T) {
	noPkg := strings.Replace(probeOut, "apk\n", "", 1)
	f := (&transport.Fake{}).On("ubus call system board", noPkg)
	s := connect(t, f, false)
	if _, err := s.PkgInstalled(context.Background()); !errors.Is(err, ErrNoPkgMgr) {
		t.Fatalf("got %v", err)
	}
}
