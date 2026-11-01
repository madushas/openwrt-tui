# Real-device test pass (phase 11c)

Run this once on each router type you care about, in this order. Everything before step 6 only
reads. Use a spare router or be ready to reach it by cable.

1. `owrt-tui doctor` - note anything REQUIRED or FEATURE that is missing. Install it first.
2. `owrt-tui probe` and `owrt-tui sample` - the model, release and counters look right.
3. Open the TUI read-only: visit every screen (Dashboard, Network, Wi-Fi, DHCP, Clients,
   Tasks, Cron, Health, Config, Changes, History, Services, Events, Logs, Packages,
   Diagnostics). Nothing should show an error toast; resize the window small and large.
4. Health: read each finding. Does each one describe your setup correctly? Mark the intended ones
   with `a`. Check the role line is right (press `r` to pin it if not). Report wrong findings.
5. Clients: names, signal and (after `sysctl -w net.netfilter.nf_conntrack_acct=1`) traffic.
6. Start with `-write`. Make one harmless change (a DHCP lease note), apply it with rollback,
   confirm, and check History shows it. Then try Undo.
7. Test the rollback: stage a change that breaks the LAN IP (on a lab router only), apply, do not
   confirm, and check the router comes back by itself.
8. WireGuard: add a device, apply, scan the QR, connect from a phone, check the handshake.
9. SIM (if used): Tasks > SIM / cellular; apply; check the interface comes up (Network),
   open a listed domain from a LAN device, watch data use in Health.
10. Cron templates: add the reconnect job, confirm it appears on the Cron screen, remove it.

Record locally (do not publish router output): the `doctor` output and a note of
any screen that looked wrong, with the terminal size. Never include credentials,
private keys, passwords, MAC addresses, public IPs, or unredacted router output.
