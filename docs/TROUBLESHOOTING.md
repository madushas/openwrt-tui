# Troubleshooting

**"permission denied" or host-key warnings when connecting.** The Connect screen can generate a key and install it with a one-time password. A changed host key is reported with the exact `ssh-keygen -R` command.

**The UI shows `?` or boxes instead of symbols.** Your terminal is not using UTF-8: run with `-icons ascii`.

**Clients show no traffic.** Byte counting is off. On the router: `sysctl -w net.netfilter.nf_conntrack_acct=1` (until reboot; add `net.netfilter.nf_conntrack_acct=1` to `/etc/sysctl.conf` to keep it).

**A change was rolled back.** Smart confirm or the rollback timer found the router unreachable or unhealthy after the change, which is the point. The History screen shows what was attempted; the Changes screen lists preflight warnings that predicted it.

**Read-only mode.** Start with `-write` to allow changes.

**Everything about a router, safely.** `owrt-tui uci` prints the configuration with secrets masked, `owrt-tui probe` shows what the router is and what it supports.
