package validate

import "testing"

func TestValidators(t *testing.T) {
	type tc struct {
		name string
		f    Func
		ok   []string
		bad  []string
	}
	for _, c := range []tc{
		{"IPv4", IPv4, []string{"192.168.1.1", "0.0.0.0"}, []string{"192.168.1", "1.2.3.256", "::1", "a.b.c.d", "1.2.3.4 ", "1.2.3.4/24"}},
		{"Netmask", Netmask, []string{"255.255.255.0", "255.255.0.0", "255.255.255.252"}, []string{"255.0.255.0", "0.0.0.0", "24", "255.255.255.256"}},
		{"CIDR4", CIDR4, []string{"192.168.1.1/24", "10.0.0.1"}, []string{"192.168.1.1/33", "1.2.3.4/", "::1/64"}},
		{"MAC", MAC, []string{"aa:bb:cc:dd:ee:ff", "AA:BB:CC:00:11:22"}, []string{"aa-bb-cc-dd-ee-ff", "aa:bb:cc:dd:ee", "gg:bb:cc:dd:ee:ff", "aabbccddeeff"}},
		{"Hostname", Hostname, []string{"laptop", "my-nas", "a.b.c", "x1"}, []string{"my_nas", "-bad", "bad-", "a b", "a..b", "$(x)", ""}},
		{"Port", Port, []string{"1", "22", "65535"}, []string{"0", "65536", "-1", "http", "22 "}},
		{"Duration", Duration, []string{"12h", "30m", "1d", "3600", "infinite", "2w"}, []string{"12 h", "h", "-1", "1.5h", "12hours"}},
		{"SSID", SSID, []string{"Home", "C:\\Trojan Horse\\Virus.exe", "日本"}, []string{"", "a\nb", "a\x1bb", "0123456789012345678901234567890123"}},
		{"WPAKey", WPAKey, []string{"password1", "correct horse battery staple", "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"}, []string{"short", "a\nbcdefgh", "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdeg0"}},
		{"Channel", Channel, []string{"auto", "1", "36", "149"}, []string{"0", "300", "x"}},
		{"UCIName", UCIName, []string{"lease_aabbcc", "host1"}, []string{"has space", "a-b", "a.b", "a;b", ""}},
		{"OneOf", OneOf("a", "b"), []string{"a", "b"}, []string{"c", "ab", ""}},
	} {
		for _, s := range c.ok {
			if err := c.f(s); err != nil {
				t.Errorf("%s(%q) = %v, want ok", c.name, s, err)
			}
		}
		for _, s := range c.bad {
			if err := c.f(s); err == nil {
				t.Errorf("%s(%q) accepted", c.name, s)
			}
		}
	}
}

func TestInSubnet(t *testing.T) {
	cases := []struct {
		ip, addr, mask string
		want           bool
	}{
		{"192.168.50.50", "192.168.50.1", "255.255.255.0", true},
		{"192.168.11.50", "192.168.50.1", "255.255.255.0", false},
		{"10.1.2.3", "10.0.0.1/8", "", true},
		{"10.1.2.3", "192.168.1.1/24", "", false},
		{"bad", "192.168.1.1", "255.255.255.0", false},
		{"192.168.1.5", "192.168.1.1", "", false},
	}
	for _, c := range cases {
		if got := InSubnet(c.ip, c.addr, c.mask); got != c.want {
			t.Errorf("InSubnet(%q,%q,%q) = %v, want %v", c.ip, c.addr, c.mask, got, c.want)
		}
	}
}
