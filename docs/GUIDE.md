# owrt-tui guide

owrt-tui configures an OpenWrt router over the SSH connection your PC already has. Nothing is installed on the router. It starts **read-only**; add `-write` to allow changes.

```
owrt-tui                       # find or choose a router, then open the UI
owrt-tui -target root@192.168.1.1 -write
owrt-tui -profile home -write  # a saved router (see ROUTERS.md)
owrt-tui -demo                 # a built-in fake router, to look around safely
owrt-tui -icons ascii          # plain-ASCII symbols (Linux console, PuTTY without UTF-8)
```

Move between screens with `1`-`9`, `0` (tenth screen) or the mouse; `:` or `ctrl+p` opens the command palette, `?` shows help.

## Screens and keys

| Screen | What it does | Keys |
|---|---|---|
| Dashboard | Live traffic, CPU, memory, clients | `i` interface, `p` pause, `+`/`-` speed, `u` bits/bytes, `j`/`k` clients, `R` reboot |
| Network | Interfaces, bridges, VLANs, routes, firewall zones, forwardings, rules, port forwards (sub-tabs) | `[` `]` sub-tab, `enter` edit, `n` new, `d` delete, `K`/`J` move a rule or port forward up/down, `g` reload |
| Wi-Fi | Radios and networks; options come from the hardware's real capabilities | `enter` edit, `t` enable/disable |
| DHCP | Pools and reservations | `enter` edit, `n` new, `s` make static, `d` delete |
| Clients | Every device in one list: leases, ARP, Wi-Fi stations, routing policy, traffic | `n` name, `s` static lease, `b` block, `v` route via VPN/WAN, `k` kick, `w` wake, `t` sort by traffic |
| Tasks | Wizards: guest network, port forward, upstream DNS, WireGuard (server, device with QR code, client), Wi-Fi schedule, scheduled reboot, backup, safe upgrade, firmware image check | `enter` start |
| Cron | The crontab, with each edit confirmed and read back | `a` add, `t` switch off/on, `d` delete |
| Health | Misconfigurations, security, flash space, temperature, VPN handshakes | `enter` details, `f` stage the fix |
| Config | Raw UCI browser and editor; secrets masked | `e` edit, `d` delete, `v` reveal |
| Changes | Review, then apply what you staged | `a` apply, `m` rollback mode, `h` smart confirm, `t` timeout, `x` clear |
| History | Every applied change, with undo | `enter` details, `r` restore |
| Services | init scripts | `s` start, `x` stop, `r` restart, `e` enable, `d` disable |
| Events | Timeline from the log: joins, leaves, DHCP, link changes, SSH logins, cron, reboots | `f` filter |
| Logs | The system log | `l` level, `/` filter, `f` follow |
| Packages | apk: search, install, remove, upgrade (dry runs first) | `i`, `u`, `/`, `+`, `-`, `U` |
| Diagnostics | ping, traceroute, DNS lookup from the router | `h` host, `p`, `t`, `n` |

## How changes stay safe

1. **Nothing is written while you edit.** Edits are *staged* and listed on the Changes screen with a risk rating.
2. **Preflight** judges the configuration *as it would be after the change*: lock-outs (changing the management address or bridge, blocking SSH, a default route that would cut you off), subnet overlap, port conflicts, dangling references. A lock-out risk forces rollback mode.
3. **Apply modes:** commit only; rollback (the router reverts by itself unless you confirm in time); or **smart confirm**, which checks the gateway, DNS, WireGuard handshake and SSH after the change, keeps it if everything is healthy and rolls back if not.
4. **History:** before each apply the unmasked previous configuration is saved (owner-only permissions) so any change can be undone; secrets are masked everywhere they are displayed. Hand edits made outside the tool show up as drift.
5. **Optional git export** (`-export-dir`) records the *masked* configuration after every apply.

Firmware flashing is deliberately not automated; the Tasks screen only checks an image with `sysupgrade -T`.

## Traffic and alerts

* Per-client traffic comes from connection tracking, so it shows what open connections have moved. Turn byte counting on once with `sysctl -w net.netfilter.nf_conntrack_acct=1` on the router; the Clients screen reminds you.
* The header shows `▲ n alert(s)` after Health finds problems or warnings.

## Terminals

Mouse, 256 colours and a UTF-8 font give the best result. Without UTF-8 (Linux console, PuTTY with a legacy character set) the UI switches to ASCII automatically; force it with `-icons ascii` or `OWRT_TUI_ICONS=ascii`. In PuTTY: Window > Translation > UTF-8 and a font such as DejaVu Sans Mono.

## SIM for one domain (Tasks > SIM / cellular)

Use this when one site needs its own mobile network while everything else uses the main
internet. The wizard creates (or reuses) the modem interface, adds it to the `wan` firewall
zone for NAT, and adds a `pbr` policy that sends only the domains you list through it.

Needs: the `pbr` package, `dnsmasq-full` (plain dnsmasq cannot track domains), and a modem
driver (`kmod-usb-net-*`, plus `uqmi` for qmi or `comgt-ncm` for ncm). The SIM interface gets
`peerdns 0` (your upstream DNS is kept) and metric 200 (the main link stays preferred). If the
main link drops, other traffic can fall back to the SIM and use data.

## Cron templates (Tasks > Cron templates)

Reconnect an interface when a ping through it fails, restart WireGuard when its handshake is
older than 5 minutes, restart or reload a service daily, reload Wi-Fi daily, keep a RAM copy of
the log. Jobs only write to /tmp, and each is tagged so it can't be added twice.

## Marking a Health finding as intended

Some findings describe choices you made on purpose (for example a 5 GHz network that shares
the `lan` zone). Press `a` on a finding to mark it as intended; it moves to the accepted list
(`A`) and no longer counts as an alert. The mark is stored per router on this PC and is tied
to the finding's exact wording: if the configuration changes so the finding changes (say the
Wi-Fi becomes open), it comes back. Findings now state the facts they rest on, such as
whether the Wi-Fi has a password, and only become problems when anyone can join.

## Device role (Health, key r)

The Health header shows the role the checks assume: router (WAN faces the internet),
firewall/VPN behind another router, access point only, or Wi-Fi repeater. It is detected from
the configuration and the WAN address (a private WAN address means another router is
upstream). Press r to pin a different role; it is stored per router. An access point or
repeater hides firewall findings; a firewall behind another router treats WAN port forwards as
notes, since they are only reachable from the upstream network.

## SIM modes

Tasks > SIM / cellular asks what uses the SIM: only listed domains, only listed devices (IPs,
networks or MACs), or nothing except as a backup when the main link is down.

## SIM-only routers

If the router's only internet is a SIM (the modem interface is in the `wan` zone and the
default route is on a modem device such as wwan0), Health uses the "router on a SIM/cellular
uplink" role. Everything else (Wi-Fi, DHCP, firewall, WireGuard client, tasks, cron) works as
on any router. Extra checks for this role: data used since boot, a note when the carrier uses
carrier-grade NAT (100.64.0.0/10, so nothing can connect in), a note about WireGuard servers
on SIM links, and port-forward findings explain that carriers usually block inbound
connections. The role is chosen from the default route's device; a USB-tethered phone (usb0)
is treated as cellular too. If the guess is wrong, press r on Health to pin a role.

## Events: repeats and reconnecting devices

The Events screen merges identical logins, DHCP leases and cron runs into one line with a
count, e.g. "SSH login for root from 192.168.50.199 (x14)". Wi-Fi joins and leaves are merged
only when they directly repeat, so a reconnect pattern stays visible. A device that joins
five or more times in the log is called out above the list (usually weak signal, power
saving on the device, or interference).

## Saved routers in the Connect screen

Routers saved with `owrt-tui profiles` are listed first in the picker with their note; Enter
connects with the saved user, host, port and key.

## DNS checks (Health)

- "devices can bypass the router's DNS": the router filters or chooses DNS (ad blocking or custom
  upstream servers) but no firewall redirect sends clients' port-53 traffic to it.
  DNS over HTTPS/TLS is not covered by that redirect.
- "DNS may be answered outside the VPN": a full-tunnel WireGuard VPN is configured but the
  router has no fixed upstream DNS with "ignore resolv file".
