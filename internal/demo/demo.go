// Package demo provides a scripted fake router so the program can be tried, and
// its output formats tested, without any hardware. It is never used for real
// connections.
package demo

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"fmt"
	"math"
	"strings"
	"sync"

	"github.com/madushakv/owrt-tui/internal/transport"
)

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

config device 'br_lan'
	option name 'br-lan'
	option type 'bridge'
	list ports 'lan1'
	list ports 'lan2'
	list ports 'lan3'

config interface 'lan'
	option device 'br-lan'
	option proto 'static'
	option ipaddr '192.168.1.1'
	option netmask '255.255.255.0'

config interface 'wan'
	option device 'wan'
	option proto 'dhcp'

config route
	option interface 'lan'
	option target '10.20.0.0/16'
	option gateway '192.168.1.254'
	option metric '10'

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

config wifi-device 'radio0'
	option type 'mac80211'
	option band '2g'
	option channel '1'
	option htmode 'HT20'

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
	option leasetime '12h'

config host
	option name 'printer'
	option mac 'aa:bb:cc:00:00:04'
	option ip '192.168.1.60'

package dropbear

config dropbear
	option Port '22'

package firewall

config zone
	option name 'lan'
	option input 'ACCEPT'
	list network 'lan'

config zone
	option name 'wan'
	option input 'REJECT'
	list network 'wan'
	list network 'wan'
	list network 'wan'

config zone
	option name 'guest'
	option input 'ACCEPT'
	list network 'guest'

config forwarding
	option src 'lan'
	option dest 'wan'

config rule
	option name 'Allow-Ping'
	option src 'wan'
	option proto 'icmp'
	option target 'ACCEPT'

config rule
	option name 'Block-Telnet'
	option src 'lan'
	option dest 'wan'
	option proto 'tcp'
	option dest_port '23'
	option target 'REJECT'

config redirect
	option name 'plex'
	option src 'wan'
	option src_dport '32400'
	option dest 'lan'
	option dest_ip '192.168.1.60'
	option proto 'tcp'
	option target 'DNAT'
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
Sun Oct 25 09:14:10 2026 daemon.info hostapd: phy0-ap0: AP-STA-CONNECTED aa:bb:cc:00:00:01 auth_alg=sae
Sun Oct 25 09:14:11 2026 daemon.info dnsmasq-dhcp[1234]: DHCPACK(br-lan) 192.168.1.50 aa:bb:cc:00:00:01 pixel-8
Sun Oct 25 09:20:44 2026 daemon.info dropbear[1011]: Pubkey auth succeeded for 'root' with key sha256:abc from 192.168.1.7:44332
Sun Oct 25 09:30:00 2026 cron.notice crond[321]: USER root pid 3039 cmd /usr/bin/ddns_update.sh >> /root/ddns.log 2>&1
Sun Oct 25 09:41:02 2026 daemon.info hostapd: phy0-ap0: AP-STA-DISCONNECTED aa:bb:cc:00:00:01
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
		OnFunc("##owrt-metrics", metricsGen()).
		On("##owrt-wifi-caps", wifiCaps).
		On("##overlay", "##overlay\noverlayfs:/overlay 14000 6000 7000 46% /overlay\n##release\n25.12.3\n##sshkeys\n1\n##cron\n0 4 * * * reboot\n*/15 * * * * /usr/bin/ddns_update.sh >> /root/ddns.log 2>&1\n##temp\n58000\n61000\n##now\n1790000100\n##wg\nwgok\nwg0\tpubkey-phone\t1790000050\n##wanip\n203.0.113.50\n##updev\nwan\n##lte\n{\"type\":\"lte\",\"rssi\":-78,\"rsrq\":-11,\"rsrp\":-108,\"snr\":4.2}\n").
		OnFunc("##acct", trafficGen()).
		On("##leases", "##leases\n1790000000 aa:bb:cc:00:00:01 192.168.1.50 pixel-8 01:aa\n1790000000 aa:bb:cc:00:00:02 192.168.1.51 laptop *\n1790000000 aa:bb:cc:00:00:04 192.168.1.60 printer *\n##neigh\n192.168.1.50 dev br-lan lladdr aa:bb:cc:00:00:01 REACHABLE\n192.168.1.51 dev br-lan lladdr aa:bb:cc:00:00:02 STALE\n192.168.1.60 dev br-lan lladdr aa:bb:cc:00:00:04 REACHABLE\n##sta phy0-ap0\nStation aa:bb:cc:00:00:01 (on phy0-ap0)\n\tinactive time:\t40 ms\n\trx bytes:\t5123456\n\ttx bytes:\t90654321\n\tsignal:  \t-52 [-52] dBm\n\tconnected time:\t3600 seconds\n").
		On("ubus call hostapd.", "").
		On("etherwake", "").
		On("ip route show", "default via 192.168.1.254 dev wan\n10.20.0.0/16 via 192.168.1.254 dev br-lan metric 10\n192.168.1.0/24 dev br-lan scope link\n").
		OnFunc("crontab", cronHandler()).
		On("sysupgrade -b", demoBackup()).
		On("sysupgrade -T", "Image metadata not present\nexit=0\n").
		On("apk update", "fetch https://downloads.openwrt.org/releases/25.12.3/packages/aarch64/packages.adb\nOK: 48 MiB in 291 packages\n").
		On("apk upgrade", "(1/2) Upgrading busybox (1.37.0-r4 -> 1.37.0-r5)\n(2/2) Upgrading dnsmasq-full (2.91-r1 -> 2.91-r2)\nOK: 48 MiB in 291 packages\n").
		On(".confirm", "confirmed\n").
		On("##owrt-postcheck", "##owrt-postcheck\ndefault_route=ok\ngateway=ok\ndns=ok\nwg_wg0=40\nproc_dnsmasq=ok\nproc_dropbear=ok\n").
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

// trafficGen returns a connection-tracking reading whose counters grow on every
// call, so the Clients screen shows live rates in demo mode.
func trafficGen() func(cmd, stdin string) transport.Result {
	var mu sync.Mutex
	n := 0
	return func(cmd, stdin string) transport.Result {
		mu.Lock()
		defer mu.Unlock()
		n++
		line := func(ip string, up, down int) string {
			return fmt.Sprintf("ipv4     2 tcp      6 300 ESTABLISHED src=%s dst=1.2.3.4 sport=5000 dport=443 packets=10 bytes=%d src=1.2.3.4 dst=203.0.113.2 sport=443 dport=5000 packets=12 bytes=%d [ASSURED] mark=0 use=2\n", ip, up, down)
		}
		out := "##acct\n1\n##ct\n"
		out += line("192.168.1.50", 400000*n, 9000000*n)
		out += line("192.168.1.51", 20000*n, 150000*n)
		out += line("192.168.1.60", 500*n, 2000*n)
		return transport.Result{Stdout: out}
	}
}

// metricsGen returns a generator whose counters advance on every call, so the
// dashboard has believable moving graphs in demo mode. Two seconds of router
// time pass per call.
func metricsGen() func(cmd, stdin string) transport.Result {
	var mu sync.Mutex
	n := 0
	cpuTotal, cpuIdle := 1e6, 8e5
	wanRx, wanTx, lanRx, lanTx, wlRx, wlTx := 4e9, 6e8, 7e8, 3.9e9, 2e8, 9e8
	return func(cmd, stdin string) transport.Result {
		mu.Lock()
		defer mu.Unlock()
		n++
		t := float64(n)
		busy := math.Max(0.02, 0.18+0.12*math.Sin(t/3)+0.08*math.Sin(t*1.7))
		cpuTotal += 800
		cpuIdle += 800 * (1 - busy)
		wave := 0.55 + 0.45*math.Sin(t/4)
		burst := 0.0
		if n%17 < 4 {
			burst = 1
		}
		down := 2 * (2.5e5 + 9e5*wave + 6e6*burst)
		upl := 2 * (6e4 + 2e5*wave*0.6 + 3e5*burst)
		wanRx += down
		wanTx += upl
		lanTx += down * 0.98
		lanRx += upl * 0.97
		wlTx += 2 * (1.2e5 + 4e5*wave)
		wlRx += 2 * (3e4 + 1e5*wave)
		return transport.Result{Stdout: fmt.Sprintf(metricsTemplate,
			93600+2*t,
			uint64((cpuTotal-cpuIdle)*0.8), 0, uint64((cpuTotal-cpuIdle)*0.2), uint64(cpuIdle),
			131072+int(9000*math.Sin(t/9)),
			51000+int(2500*math.Sin(t/11)),
			uint64(wanRx), uint64(wanTx), uint64(lanRx), uint64(lanTx), uint64(wlRx), uint64(wlTx),
			280+int(30*math.Sin(t/5)),
			1790000000+int(2*t),
		)}
	}
}

// metricsTemplate is the metrics script output of a small dual-band router.
// Verbs, in order: uptime, cpu user/nice/system/idle, MemAvailable, temp
// (millidegrees), wan rx/tx, br-lan rx/tx, wlan0 rx/tx, conntrack count,
// epoch seconds.
const metricsTemplate = `##owrt-metrics
##load
0.31 0.28 0.25 1/95 4242
##uptime
%.2f 280000.00
##cores
2
##stat
cpu  %d %d %d %d 0 0 0 0 0 0
##mem
MemTotal:         262144 kB
MemFree:           61000 kB
MemAvailable:     %d kB
Buffers:            9000 kB
Cached:            52000 kB
SwapTotal:             0 kB
SwapFree:              0 kB
##temp
%d
##net
Inter-|   Receive                                                |  Transmit
 face |bytes    packets errs drop fifo frame compressed multicast|bytes    packets errs drop fifo colls carrier compressed
    lo:  111 1 0 0 0 0 0 0  111 1 0 0 0 0 0 0
  eth1: %d 1 0 0 0 0 0 0  %d 1 0 0 0 0 0 0
br-lan: %d 1 0 0 0 0 0 0  %d 1 0 0 0 0 0 0
 wlan0: %d 1 0 0 0 0 0 0  %d 1 0 0 0 0 0 0
##ct
%d
16384
##ubus
{"interface":[
 {"interface":"loopback","up":true,"proto":"static","device":"lo"},
 {"interface":"lan","up":true,"proto":"static","uptime":93000,"l3_device":"br-lan","ipv4-address":[{"address":"192.168.1.1","mask":24}]},
 {"interface":"wan","up":true,"proto":"dhcp","uptime":92000,"l3_device":"eth1","ipv4-address":[{"address":"203.0.113.77","mask":24}],"route":[{"target":"0.0.0.0","mask":0,"nexthop":"203.0.113.1"}],"dns-server":["1.1.1.1","9.9.9.9"]}
]}
##time
%d
##leases
1790090000 aa:bb:cc:00:00:01 192.168.1.50 macbook-pro 01:aa
1790090000 aa:bb:cc:00:00:02 192.168.1.51 pixel-8 01:bb
1790090000 aa:bb:cc:00:00:03 192.168.1.52 living-room-tv 01:cc
0 aa:bb:cc:00:00:04 192.168.1.60 printer *
1790090000 aa:bb:cc:00:00:05 192.168.1.61 * 01:dd
##neigh
192.168.1.50 dev br-lan lladdr aa:bb:cc:00:00:01 REACHABLE
192.168.1.51 dev br-lan lladdr aa:bb:cc:00:00:02 REACHABLE
192.168.1.52 dev br-lan lladdr aa:bb:cc:00:00:03 STALE
192.168.1.60 dev br-lan lladdr aa:bb:cc:00:00:04 REACHABLE
##wifi
dev wlan0
wlan0     ESSID: "demo-net"
AA:BB:CC:00:00:01  -48 dBm / -95 dBm (SNR 47)  80 ms ago
	RX: 144.4 MBit/s, MCS 15
AA:BB:CC:00:00:02  -63 dBm / -95 dBm (SNR 32)  120 ms ago
AA:BB:CC:00:00:03  -74 dBm / -95 dBm (SNR 21)  300 ms ago
##end
`

// wifiCaps is "iw phy info" for a small dual-band router: 2.4 GHz HT only, and
// 5 GHz up to VHT80 with the high channels locked until a country is set.
const wifiCaps = `##phy phy0 /sys/devices/platform/soc/radio0
Wiphy phy0
	Band 1:
		Capabilities: 0x1fe
			HT20/HT40
		Frequencies:
			* 2412.0 MHz [1] (20.0 dBm)
			* 2437.0 MHz [6] (20.0 dBm)
			* 2462.0 MHz [11] (20.0 dBm)
			* 2467.0 MHz [12] (20.0 dBm) (no IR)
			* 2472.0 MHz [13] (20.0 dBm) (no IR)
##phy phy1 /sys/devices/platform/soc/radio1
Wiphy phy1
	Band 2:
		Capabilities: 0x1ff
			HT20/HT40
		VHT Capabilities (0x318001b0):
			Supported Channel Width: neither 160 nor 80+80
		Frequencies:
			* 5180.0 MHz [36] (20.0 dBm)
			* 5200.0 MHz [40] (20.0 dBm)
			* 5260.0 MHz [52] (20.0 dBm) (no IR, radar detection)
			* 5745.0 MHz [149] (20.0 dBm) (no IR)
			* 5845.0 MHz [169] (disabled)
##reg
global
country 00: DFS-UNSET
`

// cronHandler is a tiny stateful crontab: "crontab -l" reads, "crontab - &&" stores stdin.
func cronHandler() func(cmd, stdin string) transport.Result {
	var mu sync.Mutex
	tab := "0 4 * * * reboot\n*/15 * * * * /usr/bin/ddns_update.sh >> /tmp/ddns.log 2>&1\n"
	return func(cmd, stdin string) transport.Result {
		mu.Lock()
		defer mu.Unlock()
		if strings.Contains(cmd, "crontab - ") {
			tab = stdin
			return transport.Result{}
		}
		return transport.Result{Stdout: tab}
	}
}

// demoBackup is a small but valid configuration archive, base64 encoded.
func demoBackup() string {
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	body := []byte("config interface 'lan'\n")
	tw.WriteHeader(&tar.Header{Name: "etc/config/network", Mode: 0o600, Size: int64(len(body))})
	tw.Write(body)
	tw.Close()
	zw.Close()
	return base64.StdEncoding.EncodeToString(buf.Bytes()) + "\n"
}
