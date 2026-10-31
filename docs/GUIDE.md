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
