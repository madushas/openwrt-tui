package uci

import "testing"

func TestIsSecret(t *testing.T) {
	secret := []string{
		"private_key", "preshared_key", "key", "key1", "password", "passwd", "psk",
		"sae_password", "auth_pass", "token", "secret", "PRIVATE_KEY", "api_key", "wpa_psk",
	}
	notSecret := []string{
		"public_key", "pubkey", "peer_public_key", "ssid", "ipaddr", "name", "keepalive",
		"persistent_keepalive", "key5", "keyboard", "listen_port", "allowed_ips", "encryption",
	}
	for _, k := range secret {
		if !IsSecret(k) {
			t.Errorf("%q should be secret", k)
		}
	}
	for _, k := range notSecret {
		if IsSecret(k) {
			t.Errorf("%q should not be secret", k)
		}
	}
}

func TestDisplayMasksWithoutLeakingLength(t *testing.T) {
	short := Option{Key: "private_key", Values: []string{"a"}}
	long := Option{Key: "private_key", Values: []string{"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa="}}
	if short.Display(false) != MaskText || long.Display(false) != MaskText {
		t.Error("secrets must be masked by default")
	}
	if long.Display(true) != long.Value() {
		t.Error("reveal must show the real value")
	}
	pub := Option{Key: "public_key", Values: []string{"abc="}}
	if pub.Display(false) != "abc=" {
		t.Error("public keys are not secret")
	}
}
