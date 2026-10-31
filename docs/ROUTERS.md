# Several routers: profiles and compare

## Profiles

A profile is a name for a router's connection details (target, key, port). It stores no passwords and no configuration.

```
owrt-tui profiles add home root@192.168.1.1 -p 2222 -note "fibre firewall"
owrt-tui profiles add lab  root@lab-router -i ~/.ssh/lab_ed25519
owrt-tui profiles                 # list
owrt-tui profiles rm lab
owrt-tui -profile home -write     # open the UI on it
owrt-tui -profile home services   # any command works
```

`-target`, `-i` and `-p` given on the command line override the profile. The file is `profiles.json` in your user config directory (`~/.config/owrt-tui/` on Linux), owner-only; set `OWRT_TUI_PROFILES` to use another path.

## Compare

`compare` reads the configuration of two routers (read-only) and lists what differs: sections on one side only, options on one side only, and changed values. Secrets are never printed; if a key differs it says so with `********` on both sides. MAC addresses and radio paths are skipped because they differ on every device; `-all` shows them too.

```
owrt-tui compare home lab                   # two profiles (or user@host, or "demo")
owrt-tui -profile home compare lab          # the current router against lab
owrt-tui compare -config firewall,dhcp home lab
```

Use it to check that a replacement router matches the old one, or that a group of routers stayed in step. Copying settings between routers is intentionally manual: read the diff, then stage the changes on the target in the UI so preflight and rollback protect you.
