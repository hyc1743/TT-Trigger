package relay

import (
	"encoding/base64"
	"errors"
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

func TestStandaloneDeploymentMigration(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	cfg := validTestConfig()
	if err := SaveConfig(path, cfg); err != nil {
		t.Fatal(err)
	}
	legacyPath := filepath.Join(dir, "deployment.json")
	legacy := `{"mode":"public_caddy","domain":"trigger.example.com","acme_email":"admin@example.com"}`
	if err := os.WriteFile(legacyPath, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}

	migrated, changed, err := EnsureConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if !changed || migrated.Deployment == nil || migrated.Deployment.Mode != "local_tailscale" || migrated.Deployment.Domain != "" {
		t.Fatalf("deployment was not migrated: %#v", migrated.Deployment)
	}
	if _, err := os.Stat(legacyPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("legacy deployment file remains: %v", err)
	}
	if _, err := os.Stat(path + ".pre-4.0.bak"); err != nil {
		t.Fatal("pre-4.0 config backup missing")
	}
	reloaded, err := LoadConfig(path)
	if err != nil || reloaded.Deployment == nil || reloaded.Deployment.Mode != "local_tailscale" {
		t.Fatalf("integrated deployment did not reload: %#v %v", reloaded.Deployment, err)
	}
}

func TestDeploymentValidation(t *testing.T) {
	valid := validTestConfig()
	valid.Deployment = &DeploymentConfig{Mode: "local_tailscale"}
	if err := valid.Validate(); err != nil {
		t.Fatalf("local deployment rejected: %v", err)
	}
	for _, deployment := range []*DeploymentConfig{
		{Mode: "unknown"},
		{Mode: "public_caddy", Domain: "trigger.example.com"},
		{Mode: "public_caddy", Domain: "https://example.com/"},
		{Mode: "public_caddy", Domain: "example.com\nattack"},
		{Mode: "public_caddy", Domain: "example.com", ACMEEmail: "a@example.com\nattack"},
		{Mode: "local_tailscale", Domain: "example.com"},
	} {
		cfg := validTestConfig()
		cfg.Deployment = deployment
		if err := cfg.Validate(); err == nil {
			t.Fatalf("invalid deployment accepted: %#v", deployment)
		}
	}
}

func TestIntegratedPublicCaddyMigration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	cfg := validTestConfig()
	cfg.Deployment = nil
	if err := SaveConfig(path, cfg); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := strings.TrimSuffix(string(data), "\n")
	text = strings.TrimSuffix(text, "}") + ",\n  \"deployment\": {\"mode\":\"public_caddy\",\"domain\":\"trigger.example.com\"}\n}\n"
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	migrated, changed, err := EnsureConfig(path)
	if err != nil || !changed || migrated.Deployment == nil || migrated.Deployment.Mode != "local_tailscale" {
		t.Fatalf("public Caddy migration failed: %#v %v", migrated.Deployment, err)
	}
	if _, err := os.Stat(path + ".pre-4.0.bak"); err != nil {
		t.Fatal("pre-4.0 backup missing")
	}
}
