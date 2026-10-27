// Package demo provides a scripted fake router so the program can be tried, and
// its output formats tested, without any hardware. It is never used for real
// connections.
package demo

import "github.com/madushakv/owrt-tui/internal/transport"

const probe = `##board
{"kernel":"6.12.74","hostname":"gw-demo","model":"Demo Router AX3000","board_name":"demo,ax3000","rootfs_type":"squashfs","release":{"distribution":"OpenWrt","version":"25.12.3","revision":"r32912-6639b15f62","target":"mediatek/filogic"}}
##uname
aarch64
##tools
apk
nft
fw4
wg
iw
iwinfo
jsonfilter
owut
logread
ubus
uci
ip
service
sysupgrade
##swconfig
##df
Filesystem           1K-blocks      Used Available Use% Mounted on
overlayfs:/overlay       60000     21000     39000  35% /overlay
tmpfs                   262144      3000    259144   1% /tmp
##uptime
1043210.55 2000000.00
`

// Throwaway demo values; none of these are real credentials.
const uciExport = `package network

config interface 'lan'
	option device 'br-lan'
	option proto 'static'
	option ipaddr '192.168.1.1'
	option netmask '255.255.255.0'

config interface 'wg0'
	option proto 'wireguard'
	option private_key 'DEMOdemoDEMOdemoDEMOdemoDEMOdemoDEMOdemo000='
	option listen_port '51820'
	list addresses '10.9.0.1/24'

config wireguard_wg0
	option description 'phone'
	option public_key 'PUBLICpublicPUBLICpublicPUBLICpublic0000='
	option preshared_key 'DEMOpskDEMOpskDEMOpskDEMOpskDEMOpsk000='
	list allowed_ips '10.9.0.2/32'

package wireless

config wifi-iface 'default_radio0'
	option device 'radio0'
	option mode 'ap'
	option ssid 'HomeNet'
	option encryption 'sae-mixed'
	option key 'demo-wifi-passphrase'

package dhcp

config dnsmasq
	option domainneeded '1'
	list server '1.1.1.1'
	list server '9.9.9.9'

config dhcp 'lan'
	option interface 'lan'
	option start '100'
	option limit '150'
`

const services = `svc dnsmasq 1 19
svc dropbear 1 19
svc firewall 1 19
svc network 1 20
svc uhttpd 1 50
svc wpad 1 19
##ubus
{"dnsmasq":{"instances":{"i":{"running":true}}},"dropbear":{"instances":{"i":{"running":true}}},"uhttpd":{"instances":{"i":{"running":true}}},"network":{"instances":{"i":{"running":false}}}}
`

const logread = `Sun Oct 25 09:13:01 2026 kern.info kernel: [    1.2] mt7915e 0000:00:00.0: HW/SW Version: 0x8a108a10
Sun Oct 25 09:13:02 2026 daemon.info dnsmasq-dhcp[1234]: DHCPACK(br-lan) 192.168.1.50 aa:bb:cc:dd:ee:ff phone
Sun Oct 25 09:13:03 2026 daemon.err dropbear[999]: Bad password attempt for 'root' from 203.0.113.9:5555
Sun Oct 25 09:13:04 2026 daemon.warn netifd: Interface 'wan' has link connectivity loss
Sun Oct 25 09:13:09 2026 daemon.notice netifd: Interface 'wan' is now up
`

const installed = `busybox-1.37.0-r4 aarch64 {busybox} (GPL-2.0-only) [installed]
dnsmasq-full-2.91-r1 aarch64 {dnsmasq} (GPL-2.0-or-later) [installed]
kmod-8021q-6.12.74-r1 aarch64 {linux} (GPL-2.0-only) [installed]
wireguard-tools-1.0.20250521-r1 aarch64 {wireguard-tools} (GPL-2.0-only) [installed]
`

const upgradable = `busybox-1.37.0-r5 aarch64 {busybox} (GPL-2.0-only) [upgradable from: busybox-1.37.0-r4]
`

const search = `luci-proto-wireguard-26.100.1-r1 - Support for WireGuard VPN
wireguard-tools-1.0.20250521-r1 - WireGuard userspace control program (wg)
kmod-wireguard-6.12.74-r1 - WireGuard kernel module
luci-app-statistics-26.100.1-r1 - Statistics application; can show wireguard peers
`

const ping = `PING 1.1.1.1 (1.1.1.1): 56 data bytes
64 bytes from 1.1.1.1: seq=0 ttl=57 time=9.6 ms

--- 1.1.1.1 ping statistics ---
4 packets transmitted, 4 packets received, 0% packet loss
round-trip min/avg/max = 9.1/9.6/10.4 ms
`

// Router returns a fake that answers the commands the program issues.
func Router() *transport.Fake {
	return (&transport.Fake{Name: "demo"}).
		On("ubus call system board", probe).
		On("uci export", uciExport).
		On("ubus call service list", services).
		On("logread -l", logread).
		On("apk list --installed", installed).
		On("apk list --upgradeable", upgradable).
		On("apk search", search).
		On("apk info --all", "wireguard-tools-1.0.20250521-r1 description:\nWireGuard userspace control program (wg)\n").
		On("apk add --simulate", "(1/2) Installing kmod-wireguard (6.12.74-r1)\n(2/2) Installing wireguard-tools (1.0.20250521-r1)\nOK: 48 MiB in 291 packages\n").
		On("apk del --simulate", "(1/1) Purging wireguard-tools (1.0.20250521-r1)\nOK: 47 MiB in 290 packages\n").
		On("ping -c", ping).
		On("traceroute", "traceroute to 1.1.1.1\n 1  192.168.1.254  0.9 ms\n 2  100.64.0.1  4.1 ms\n 3  1.1.1.1  9.6 ms\n").
		On("nslookup", "Server:\t\t127.0.0.1\nName:\texample.com\nAddress: 93.184.216.34\n")
}
