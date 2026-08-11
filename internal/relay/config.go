package relay

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
)

const (
	defaultExtensionListen      = "127.0.0.1:8787"
	defaultAPIListen            = "127.0.0.1:8788"
	defaultTriggerTimeoutMS     = 5000
	defaultSignatureMaxSkewSecs = 30
)

type HMACKey struct {
	ID     string `json:"id"`
	Secret string `json:"secret"`
}

type Config struct {
	ExtensionListen         string    `json:"extension_listen"`
	APIListen               string    `json:"api_listen"`
	ExtensionToken          string    `json:"extension_token"`
	HMACKeys                []HMACKey `json:"hmac_keys"`
	SignatureMaxSkewSeconds int       `json:"signature_max_skew_seconds"`
	TriggerTimeoutMS        int       `json:"trigger_timeout_ms"`

	LegacyToken         string `json:"token,omitempty"`
	LegacyListen        string `json:"listen,omitempty"`
	AdditionalAPIListen string `json:"-"`
}

func randomSecret() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func GenerateHMACKey(id string) (HMACKey, error) {
	if !validKeyID(id) {
		return HMACKey{}, errors.New("key id must contain 1-64 letters, digits, dots, underscores, or hyphens")
	}
	secret, err := randomSecret()
	if err != nil {
		return HMACKey{}, err
	}
	return HMACKey{ID: id, Secret: secret}, nil
}

func DefaultConfig() (Config, error) {
	extensionToken, err := randomSecret()
	if err != nil {
		return Config{}, fmt.Errorf("generate extension token: %w", err)
	}
	hmacSecret, err := randomSecret()
	if err != nil {
		return Config{}, fmt.Errorf("generate HMAC secret: %w", err)
	}
	return Config{
		ExtensionListen:         defaultExtensionListen,
		APIListen:               defaultAPIListen,
		ExtensionToken:          extensionToken,
		HMACKeys:                []HMACKey{{ID: "default", Secret: hmacSecret}},
		SignatureMaxSkewSeconds: defaultSignatureMaxSkewSecs,
		TriggerTimeoutMS:        defaultTriggerTimeoutMS,
	}, nil
}

// EnsureConfig creates a new configuration or migrates a pre-3.0 configuration.
func EnsureConfig(path string) (Config, bool, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		cfg, genErr := DefaultConfig()
		if genErr != nil {
			return Config{}, false, genErr
		}
		if err := saveConfig(path, cfg); err != nil {
			return Config{}, false, err
		}
		return cfg, true, nil
	}
	if err != nil {
		return Config{}, false, err
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Config{}, false, fmt.Errorf("decode config: %w", err)
	}
	changed := cfg.normalize()
	if cfg.ExtensionToken == "" && cfg.LegacyToken != "" {
		cfg.ExtensionToken = cfg.LegacyToken
		changed = true
	}
	if len(cfg.HMACKeys) == 0 {
		secret, genErr := randomSecret()
		if genErr != nil {
			return Config{}, false, fmt.Errorf("generate HMAC secret: %w", genErr)
		}
		cfg.HMACKeys = []HMACKey{{ID: "default", Secret: secret}}
		changed = true
	}
	if cfg.LegacyToken != "" || cfg.LegacyListen != "" {
		cfg.LegacyToken = ""
		cfg.LegacyListen = ""
		changed = true
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, false, err
	}
	if changed {
		backup := path + ".pre-3.0.bak"
		if _, statErr := os.Stat(backup); errors.Is(statErr, os.ErrNotExist) {
			if err := os.WriteFile(backup, data, 0o600); err != nil {
				return Config{}, false, fmt.Errorf("back up configuration: %w", err)
			}
		} else if statErr != nil {
			return Config{}, false, fmt.Errorf("check configuration backup: %w", statErr)
		}
		if err := saveConfig(path, cfg); err != nil {
			return Config{}, false, err
		}
	}
	return cfg, changed, nil
}

func LoadConfig(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("decode config: %w", err)
	}
	cfg.normalize()
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func SaveConfig(path string, cfg Config) error {
	if err := cfg.Validate(); err != nil {
		return err
	}
	return saveConfig(path, cfg)
}

func saveConfig(path string, cfg Config) error {
	if dir := filepath.Dir(path); dir != "." {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("create config directory: %w", err)
		}
	}
	cfg.LegacyToken = ""
	cfg.LegacyListen = ""
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("encode config: %w", err)
	}
	data = append(data, '\n')
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	_ = os.Chmod(tmp, 0o600)
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("replace config: %w", err)
	}
	_ = os.Chmod(path, 0o600)
	return nil
}

func (c Config) Validate() error {
	if err := validateLoopbackListen("extension_listen", c.ExtensionListen); err != nil {
		return err
	}
	if err := validateLoopbackListen("api_listen", c.APIListen); err != nil {
		return err
	}
	if c.ExtensionListen == c.APIListen {
		return errors.New("extension_listen and api_listen must use different addresses")
	}
	if strings.TrimSpace(c.AdditionalAPIListen) != "" {
		if err := validateAPIListen(c.AdditionalAPIListen); err != nil {
			return fmt.Errorf("additional API listen address: %w", err)
		}
		if c.AdditionalAPIListen == c.APIListen || c.AdditionalAPIListen == c.ExtensionListen {
			return errors.New("additional API listen address must be unique")
		}
	}
	if len(c.ExtensionToken) < 32 {
		return errors.New("extension_token must contain at least 32 characters")
	}
	if len(c.HMACKeys) == 0 {
		return errors.New("hmac_keys must contain at least one key")
	}
	seen := make(map[string]bool, len(c.HMACKeys))
	for _, key := range c.HMACKeys {
		if !validKeyID(key.ID) || seen[key.ID] {
			return fmt.Errorf("invalid or duplicate HMAC key id %q", key.ID)
		}
		seen[key.ID] = true
		decoded, err := base64.RawURLEncoding.DecodeString(key.Secret)
		if err != nil || len(decoded) != 32 {
			return fmt.Errorf("HMAC key %q secret must be 32 bytes encoded as unpadded Base64URL", key.ID)
		}
	}
	if c.SignatureMaxSkewSeconds < 5 || c.SignatureMaxSkewSeconds > 300 {
		return errors.New("signature_max_skew_seconds must be between 5 and 300")
	}
	if c.TriggerTimeoutMS < 250 || c.TriggerTimeoutMS > 60000 {
		return errors.New("trigger_timeout_ms must be between 250 and 60000")
	}
	return nil
}

func validKeyID(value string) bool {
	if len(value) < 1 || len(value) > 64 {
		return false
	}
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || strings.ContainsRune("._-", r) {
			continue
		}
		return false
	}
	return true
}

func validateAPIListen(value string) error {
	host, _, err := net.SplitHostPort(strings.TrimSpace(value))
	if err != nil {
		return fmt.Errorf("api listen must be a host:port address: %w", err)
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return errors.New("additional API listen must use an IP address")
	}
	_, tailscaleRange, _ := net.ParseCIDR("100.64.0.0/10")
	if !tailscaleRange.Contains(ip) {
		return errors.New("additional API listen must use a Tailscale IPv4 address")
	}
	return nil
}

func (c *Config) normalize() bool {
	changed := false
	if strings.TrimSpace(c.ExtensionListen) == "" {
		c.ExtensionListen = defaultExtensionListen
		changed = true
	}
	if strings.TrimSpace(c.APIListen) == "" {
		c.APIListen = defaultAPIListen
		changed = true
	}
	if c.SignatureMaxSkewSeconds == 0 {
		c.SignatureMaxSkewSeconds = defaultSignatureMaxSkewSecs
		changed = true
	}
	if c.TriggerTimeoutMS == 0 {
		c.TriggerTimeoutMS = defaultTriggerTimeoutMS
		changed = true
	}
	return changed
}

func validateLoopbackListen(name, value string) error {
	host, _, err := net.SplitHostPort(strings.TrimSpace(value))
	if err != nil {
		return fmt.Errorf("%s must be a host:port address: %w", name, err)
	}
	if host == "localhost" {
		return nil
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("%s must use a loopback address", name)
	}
	return nil
}
