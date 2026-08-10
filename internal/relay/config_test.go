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
	valid := Config{Listen: "127.0.0.1:8787", Token: "01234567890123456789012345678901", TriggerTimeoutMS: 5000}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}

	cases := []Config{
		{Listen: "", Token: valid.Token, TriggerTimeoutMS: 5000},
		{Listen: valid.Listen, Token: "short", TriggerTimeoutMS: 5000},
		{Listen: valid.Listen, Token: valid.Token, TriggerTimeoutMS: 100},
	}
	for _, cfg := range cases {
		if err := cfg.Validate(); err == nil {
			t.Fatalf("invalid config accepted: %#v", cfg)
		}
	}
}
