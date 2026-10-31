# owrt-tui

A terminal UI for managing OpenWrt routers over SSH. Agentless: nothing is
installed on the router. Targets OpenWrt 25.12 (apk) with an opkg backend for
24.10 and older.

## Status: Phase 10 (reach and polish)

| Piece | State |
|---|---|
| SSH transport (system `ssh`, ControlMaster) | verified on a real router (OpenWrt 25.12.2, apk, fw4): `probe`, `sample`, `services`, `logs`, `uci`, `pkg installed` work. Intermittent exit 255 with no message on back-to-back runs: fixed (`Close` now waits for the shared connection to exit; the read-only CLI retries once). |
| Capability probe (release, apk/opkg, fw3/fw4, space, tools, EOL advisories) | done, tested |
| UCI export parser, staged change set, diff, lockout guard | done, tested |
| Apply with rollback watchdog, confirm, restore | done, script tested with a fake router; **not yet run on a real device** |
| Package manager: apk + opkg, search ranking, mandatory `--simulate` | done, tested |
| Init script manager (list, state, start/stop/restart/enable/disable) | done, tested |
| Log reader and filter, follow mode | done, tested (follow streaming needs real ssh) |
| Diagnostics: ping, traceroute, nslookup | done, tested |
| Secret masking (`private_key`, `psk`, passwords, ...) | done, tested |
| Read-only mode, enforced in `internal/session` | done, mutation-tested |
| Reboot / power off (detached so ssh exits cleanly) | done, tested |
| TUI (Bubble Tea v2): Overview, Config explorer with staged edits, Changes (diff, risk, apply with rollback and confirm), Services, Logs (filter, follow), Packages (search, info, mandatory dry run, install/remove), Diagnostics | done; tested with the demo router (rendering, secret masking, read-only guard, confirm-before-send). |

## Terminal UI

```
./owrt-tui -demo                         # try it with no router
./owrt-tui -target root@192.168.1.1      # read-only
./owrt-tui -target root@192.168.1.1 -write   # allow staging and applying changes
```

With no `-target`, the UI opens a Connect screen. It lists your last used router,
your default gateway (labelled as a guess), and the Hosts in `~/.ssh/config`.
**Nothing is contacted until you press enter.** If the connection fails, it says why
and what to do:

| Failure | What happens |
|---|---|
| Unknown host key | shows the fingerprint; on `y` it saves exactly that key to known_hosts, then connects |
| Host key changed | explains and stops; no way through (remove the old entry yourself with `ssh-keygen -R`) |
| Key not accepted | key wizard: pick or create an ed25519 key and install it on the router. `ssh` and `ssh-keygen` run in your terminal, so passwords and passphrases never reach this program |
| Refused / timeout / unreachable | explains the likely causes; retry or try another address |
| Answers but is not OpenWrt | says so (the gateway is often a modem or switch) |

After a successful connection through the gateway or a typed address it offers to add a
`Host` entry to `~/.ssh/config` (append-only, asks first). The last used targets are kept in
your user config dir (`owrt-tui/recent`).

Keys: tab or 1-7 switch screens, ? help, q quit. Each screen lists its keys in the footer.

## Dashboard and navigation

The home screen is a live dashboard (refreshes every 2 s while visible; it pauses on other screens):
system facts, CPU (total and per core) with history, memory, flash, connection tracking, temperature,
interfaces with state and addresses, a throughput graph for the WAN (or any interface, `i` to cycle),
per-device rates, and connected clients with Wi-Fi signal bars. `p` pauses, `+`/`-` change the refresh
speed, `u` switches bits/bytes. It only reads: one batched command per refresh.

Around it: a menu on the left, a detail pane on wide terminals (services, config values with secrets
masked), mouse support (click the menu, scroll with the wheel), a command palette (`:` or Ctrl+P, fuzzy
search for any screen or action, such as "restart dnsmasq"), a help overlay (`?`) and three colour themes
(dark, light, colour-blind safe; switch from the palette, the choice is remembered).

## Try it on your router (read-only)

The command-line front end always connects read-only, so a first run cannot
change anything. It uses your system `ssh`, so your `~/.ssh/config`, agent and
ProxyJump all apply. It never prompts, so use a key or agent (BatchMode).

```
./owrt-tui -demo probe                        # no router needed
./owrt-tui -target root@192.168.1.1 probe     # what the router is and can do
./owrt-tui -target root@192.168.1.1 services
./owrt-tui -target root@192.168.1.1 uci       # secrets masked; add -reveal to show
./owrt-tui -target root@192.168.1.1 logs -level warn -n 200
./owrt-tui -target root@192.168.1.1 logs -f   # Ctrl-C to stop
./owrt-tui -target root@192.168.1.1 pkg plan-add wireguard-tools   # dry run only
./owrt-tui -target root@192.168.1.1 ping 1.1.1.1
```

Unix only for now (ssh ControlMaster sockets): Linux, macOS, WSL.

## Layout

```
internal/transport   Run / Stream over ssh, local shell, or a scripted Fake
internal/shq         shell quoting and identifier validation (every remote
                     command interpolates only validated or quoted values)
internal/device      capability probe
internal/uci         `uci export` parser, secret masking
internal/changes     staged changes, diff, guard, apply/rollback scripts
internal/pkgmgr      apk and opkg backends
internal/services    init scripts
internal/logs        logread parsing, filtering, follow
internal/diag        ping, traceroute, nslookup
internal/session     the facade the UI calls; owns the read-only switch
```

The UI should only ever call `internal/session`. Every method that changes the
router checks read-only mode there, and a test asserts that read-only mode sends
no commands at all.

## Develop

```
make check      # vet, race-enabled tests, gofmt
```

Tests use `transport.Fake`, so no router is needed.

## DHCP and static leases (phase 3a)

The **DHCP** screen lists address pools, static leases and live leases that have no reservation yet.
`s` turns a live lease into a static lease (name, MAC and IP prefilled; hostnames from the router are
sanitised), `n` adds one by hand, `enter` edits a pool or reservation, `d` deletes a reservation.
Everything is validated as you type, duplicates of an existing MAC or IP are refused, and the result is
only *staged*: review and apply it on the Changes screen. Read-only mode blocks the forms entirely.

## Wi-Fi (phase 3b)

The **Wi-Fi** screen lists each radio with the networks on it. `enter` edits a radio (channel, width, country,
off) or a network (name, security, password, hidden, client isolation, off); `t` turns one on or off.
Passwords are never loaded, shown, echoed or put in the diff: leaving the field empty keeps the current one,
and a network with no password cannot be switched to WPA without entering one. Channels are checked against the band.

The Wi-Fi radio form reads each radio's real capabilities (`iw phy info`, read-only): only the channels and
widths the chip supports are offered, DFS and country-locked channels are labelled, and a missing country
code (world region) is called out.

## Safe apply, history and drift (phase 4)

- **Preflight** compares the configuration before and after your staged changes and reports only
  problems the change introduces (lock-out of the management interface or SSH, overlapping subnets,
  DHCP pool outside the subnet, duplicate static leases, port conflicts, Wi-Fi without a key). A
  lock-out finding forces rollback mode.
- **Smart confirm** (key `h` on Changes): a health check is taken before the apply and again after;
  if gateway, DNS, WireGuard handshakes and key services are healthy twice in a row, the change is
  confirmed automatically. If something regressed, nothing is confirmed and the router rolls back.
- **History** (screen 7): every apply is recorded on this PC. `r` stages the exact undo, which then
  goes through preflight, rollback and smart confirm like any other change.
- **Drift**: a fingerprint of every config is stored after each apply. If the router differs on the
  next start (changed by LuCI, SSH, or a script), you are told which configs changed; `a` accepts it.
- **Git export** (opt in): `-export-dir ~/router-configs` commits the *masked* configuration after
  every apply, so `git log -p` shows your router's whole history.

**Privacy:** History snapshots hold the real configuration, including Wi-Fi passwords and VPN keys,
because an undo needs them. They live under your user config dir (`owrt-tui/history/<router>/`),
directory mode 0700, files 0600, and never leave your PC. The git export is masked.

## Health (phase 5)

The Health screen (tab, or `:` then "Go to Health") checks the configuration and a few read-only runtime
facts and lists problems. `f` stages the fix as ordinary changes, so it still goes through preflight,
rollback and smart confirm. Fixes are only offered when they are safe: a guest zone is tightened only
if DHCP and DNS rules for it exist, and SSH passwords are turned off only if a key is installed.
Checks: duplicate list entries, an interface in two zones, a guest zone that can reach the router,
admin ports open to the internet, missing Wi-Fi country code, SSH password login, flash space, frequent
cron jobs writing to flash (credentials in cron lines are masked), enabled-but-stopped services.
Screens past the ninth have no number key; `0` opens the tenth, and `tab` or the palette reach the rest.

## Clients (phase 6)

One list of every device: DHCP leases, neighbour table, Wi-Fi stations (signal, traffic, time connected),
static reservations, block rules and routing policy merged by MAC address. Keys: `n` name, `s` make
static, `b` block/unblock (a firewall rule named `owrt_block_<mac>`; the device can still reach the
router), `v` route through the VPN or WAN (needs pbr; creates policy `owrt_route_<mac>`), `k` kick from
Wi-Fi, `w` wake-on-LAN (needs `etherwake` on the router). Name/static/block/route are staged changes;
kick and wake are immediate and confirmed.

## Tasks (phase 7)

Guided jobs that stage ordinary changes (so preflight, rollback and smart confirm still apply):
- **Guest / free Wi-Fi**: new interface and subnet, DHCP, a firewall zone that rejects input and forward
  (only DHCP and DNS are allowed to the router; the only forwarding is to wan), and the SSID with
  optional client isolation. Refuses a name, subnet or SSID that clashes with what exists.
- **Port forward**: checks the address and ports, refuses a duplicate external port, warns about SSH,
  web, RDP and file-sharing ports.
- **Upstream DNS**: Cloudflare, Quad9, Google or AdGuard; warns if adblock or https-dns-proxy is present.

- **WireGuard server / add a device / client.** Keys are generated on your PC (checked against the RFC 7748
  test vector). A device's config is shown once as a QR code you scan with the WireGuard app (the QR encoder
  is built in and was verified against a reference encoder and by decoding); its private key is never sent
  to the router and is stored only if you press `w` (a private 0600 file). The client wizard warns when a
  second tunnel would also claim the default route (use pbr to split clients).
- **Wi-Fi schedule / scheduled reboot / Cron screen.** Edits the root crontab: your other lines are
  untouched, the old crontab is kept in `/tmp/owrt-tui` on the router, cron is enabled and restarted, and
  the result is read back and compared. The Wi-Fi schedule uses `wifi down/up <radio>` (runtime only, no
  flash writes). Frequent jobs that write to flash are flagged. Cron changes are not UCI, so they do not
  go through the Changes screen: each one is confirmed on its own.
- **Back up to this PC**: `sysupgrade -b` archive, validated, stored under your config dir (0700/0600).
- **Safe package upgrade** (apk only): flash-space check, backup first, upgrade, health compared before and
  after. **Firmware image check** runs `sysupgrade -T` on an image you uploaded. owrt-tui does *not* flash
  firmware: a failed flash cannot be rolled back remotely.

## Network and firewall editors (phase 8)

The Network screen has sections; `[` and `]` move between them: **Interfaces, Bridges, VLANs, Routes, Zones,
Forwards, Rules, Port fwd**. In each: `enter` edit, `n` new, `d` delete, `t` switch a rule or forward off/on.
Everything is staged on the Changes screen first, and a long list (ports, networks) is rewritten the way UCI
needs (delete + add_list).

- **Interfaces**: create static, DHCP, DHCPv6, PPPoE or unmanaged interfaces, optionally put them in a firewall
  zone and start a DHCP pool. Deleting one also removes it from firewall zones and deletes its DHCP pool and
  routes; Wi-Fi networks attached to it are reported, not changed. WireGuard interfaces are managed under Tasks.
- **Bridges / VLANs**: a port cannot be in two bridges; deleting a bridge is refused while an interface uses it.
- **Routes**: shows the router's live routing table under the configured routes.
- **Firewall**: zones (a zone that rules or forwards still use cannot be deleted), forwardings, rules (with
  source/destination address, MAC, ports, action, family) and port forwards.

Safety: the preflight rules added in this phase run on the *resulting* configuration. They force the rollback
timer for changes to the ports or VLANs of the bridge your session uses, for a rule that rejects or drops the
management zone's traffic to the router, and for any static default route. They also flag references to zones
or interfaces that do not exist, duplicate zones, duplicate VLANs or routes, and gateways outside their
interface's network.

Not covered yet: IPv6 route editing, ip rules (policy routing outside pbr), and re-ordering firewall rules
(new rules are appended).

## Icons and ASCII mode

The sidebar uses Unicode symbols. On the Linux console, PuTTY with a non-UTF-8
character set, or any terminal without those glyphs, the UI switches to plain
ASCII automatically (non-UTF-8 locale, or `TERM=linux`/`dumb`). Force it with
`-icons ascii` (or `OWRT_TUI_ICONS=ascii`); `-icons unicode` forces Unicode.
In ASCII mode borders become `+-|`, bars `#`, and the WireGuard QR code is drawn
with coloured spaces instead of half-block characters. For PuTTY, the nicest
result is Window > Translation > UTF-8 plus a font such as DejaVu Sans Mono.

## Observability

- **Events** turns the system log into a timeline: devices joining and leaving Wi-Fi, DHCP leases, interfaces and links going up and down, SSH logins (failed ones in red, with a banner for repeated failures), cron runs and reboots. `f` cycles the filter.
- **Clients** has a TRAFFIC column. It reads the connection-tracking table, so it shows what open connections have moved. Press `g` twice for live rates and `t` to sort by usage. If byte counting is off the screen shows the one command that turns it on (`sysctl -w net.netfilter.nf_conntrack_acct=1`).
- **Health** also watches temperature, stale outbound WireGuard tunnels, and cellular signal. The header shows `▲ n alert(s)` after a Health check finds problems or warnings.

## More

- [Guide](docs/GUIDE.md): every screen and key, and how changes stay safe
- [Several routers](docs/ROUTERS.md): saved profiles and `compare`
- [Troubleshooting](docs/TROUBLESHOOTING.md)
- Releases: push a tag like `v0.1.0`; GitHub Actions builds archives for Linux (amd64, arm64, arm, mipsle), macOS and Windows and publishes them with `SHA256SUMS`. Locally: `make release VERSION=v0.1.0`. Verify a download with `sha256sum -c SHA256SUMS --ignore-missing`.
