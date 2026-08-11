package relay

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func validTestConfig() Config {
	return Config{
		ExtensionListen:         "127.0.0.1:8787",
		APIListen:               "127.0.0.1:8788",
		ExtensionToken:          strings.Repeat("e", 32),
		HMACKeys:                []HMACKey{{ID: "default", Secret: base64.RawURLEncoding.EncodeToString([]byte(strings.Repeat("h", 32)))}},
		SignatureMaxSkewSeconds: 30,
		TriggerTimeoutMS:        5000,
	}
}

func TestEnsureConfigCreatesAndReloads(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	createdConfig, changed, err := EnsureConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("expected a new config")
	}
	if len(createdConfig.ExtensionToken) < 32 || len(createdConfig.HMACKeys) != 1 {
		t.Fatalf("generated credentials are invalid: %#v", createdConfig)
	}
	loadedConfig, changedAgain, err := EnsureConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if changedAgain {
		t.Fatal("existing config must not be replaced")
	}
	if loadedConfig.ExtensionToken != createdConfig.ExtensionToken || loadedConfig.HMACKeys[0] != createdConfig.HMACKeys[0] {
		t.Fatal("credentials changed while reloading")
	}
}

func TestConfigValidation(t *testing.T) {
	valid := validTestConfig()
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}
	dual := valid
	dual.AdditionalAPIListen = "100.86.12.34:8788"
	if err := dual.Validate(); err != nil {
		t.Fatalf("Tailscale listen rejected: %v", err)
	}
	cases := []Config{valid, valid, valid, valid, valid}
	cases[0].ExtensionListen = "0.0.0.0:8787"
	cases[1].APIListen = "100.86.12.34:8788"
	cases[2].AdditionalAPIListen = "192.168.1.5:8788"
	cases[3].SignatureMaxSkewSeconds = 1
	cases[4].HMACKeys = []HMACKey{{ID: "bad id", Secret: valid.HMACKeys[0].Secret}}
	for _, cfg := range cases {
		if err := cfg.Validate(); err == nil {
			t.Fatalf("invalid config accepted: %#v", cfg)
		}
	}
}

func TestLegacyConfigMigration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	legacyToken := "01234567890123456789012345678901"
	data := []byte(`{"listen":"0.0.0.0:8787","token":"` + legacyToken + `","trigger_timeout_ms":5000}`)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, changed, err := EnsureConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if !changed || cfg.ExtensionToken != legacyToken || len(cfg.HMACKeys) != 1 {
		t.Fatalf("legacy config migration failed: %#v", cfg)
	}
	if _, err := os.Stat(path + ".pre-3.0.bak"); err != nil {
		t.Fatal("migration backup missing")
	}
	stored, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(stored), `"token"`) || strings.Contains(string(stored), `"listen"`) {
		t.Fatalf("legacy fields remain: %s", stored)
	}
}
