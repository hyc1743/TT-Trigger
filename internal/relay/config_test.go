package relay

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEnsureConfigCreatesAndReloads(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	createdConfig, created, err := EnsureConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if !created {
		t.Fatal("expected a new config")
	}
	if len(createdConfig.Token) < 32 {
		t.Fatalf("generated token is too short: %d", len(createdConfig.Token))
	}

	loadedConfig, createdAgain, err := EnsureConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if createdAgain {
		t.Fatal("existing config must not be replaced")
	}
	if loadedConfig != createdConfig {
		t.Fatalf("loaded config differs: %#v != %#v", loadedConfig, createdConfig)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
}

func TestConfigValidation(t *testing.T) {
	valid := Config{
		ExtensionListen:  "127.0.0.1:8787",
		APIListen:        "127.0.0.1:8788",
		Token:            "01234567890123456789012345678901",
		TriggerTimeoutMS: 5000,
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}
	tailscale := valid
	tailscale.APIListen = "100.86.12.34:8788"
	if err := tailscale.Validate(); err != nil {
		t.Fatalf("Tailscale API listen rejected: %v", err)
	}

	cases := []Config{
		{ExtensionListen: "0.0.0.0:8787", APIListen: valid.APIListen, Token: valid.Token, TriggerTimeoutMS: 5000},
		{ExtensionListen: valid.ExtensionListen, APIListen: "0.0.0.0:8788", Token: valid.Token, TriggerTimeoutMS: 5000},
		{ExtensionListen: valid.ExtensionListen, APIListen: "192.168.1.5:8788", Token: valid.Token, TriggerTimeoutMS: 5000},
		{ExtensionListen: valid.ExtensionListen, APIListen: valid.APIListen, Token: "short", TriggerTimeoutMS: 5000},
		{ExtensionListen: valid.ExtensionListen, APIListen: valid.APIListen, Token: valid.Token, TriggerTimeoutMS: 100},
	}
	for _, cfg := range cases {
		if err := cfg.Validate(); err == nil {
			t.Fatalf("invalid config accepted: %#v", cfg)
		}
	}
}

func TestLegacyPublicListenMigratesToLoopbackDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.json")
	data := []byte(`{"listen":"0.0.0.0:8787","token":"01234567890123456789012345678901","trigger_timeout_ms":5000}`)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ExtensionListen != defaultExtensionListen || cfg.APIListen != defaultAPIListen {
		t.Fatalf("legacy config was not migrated safely: %#v", cfg)
	}
	if cfg.LegacyListen != "" {
		t.Fatalf("legacy listen value was retained: %#v", cfg)
	}
}
