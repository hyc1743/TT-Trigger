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
	defaultExtensionListen  = "127.0.0.1:8787"
	defaultAPIListen        = "127.0.0.1:8788"
	defaultTriggerTimeoutMS = 5000
)

type Config struct {
	ExtensionListen  string `json:"extension_listen"`
	APIListen        string `json:"api_listen"`
	Token            string `json:"token"`
	TriggerTimeoutMS int    `json:"trigger_timeout_ms"`
	LegacyListen     string `json:"listen,omitempty"`
}

func DefaultConfig() (Config, error) {
	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		return Config{}, fmt.Errorf("generate token: %w", err)
	}
	return Config{
		ExtensionListen:  defaultExtensionListen,
		APIListen:        defaultAPIListen,
		Token:            base64.RawURLEncoding.EncodeToString(tokenBytes),
		TriggerTimeoutMS: defaultTriggerTimeoutMS,
	}, nil
}

func EnsureConfig(path string) (Config, bool, error) {
	cfg, err := LoadConfig(path)
	if err == nil {
		return cfg, false, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return Config{}, false, err
	}

	cfg, err = DefaultConfig()
	if err != nil {
		return Config{}, false, err
	}
	if dir := filepath.Dir(path); dir != "." {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return Config{}, false, fmt.Errorf("create config directory: %w", err)
		}
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return Config{}, false, fmt.Errorf("encode config: %w", err)
	}
	data = append(data, '\n')
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			loaded, loadErr := LoadConfig(path)
			return loaded, false, loadErr
		}
		return Config{}, false, fmt.Errorf("create config: %w", err)
	}
	defer f.Close()
	if _, err := f.Write(data); err != nil {
		return Config{}, false, fmt.Errorf("write config: %w", err)
	}
	return cfg, true, nil
}

func LoadConfig(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	var cfg Config
	decErr := json.Unmarshal(data, &cfg)
	if decErr != nil {
		return Config{}, fmt.Errorf("decode config: %w", decErr)
	}
	cfg.normalize()
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
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
	if len(c.Token) < 32 {
		return errors.New("token must contain at least 32 characters")
	}
	if c.TriggerTimeoutMS < 250 || c.TriggerTimeoutMS > 60000 {
		return errors.New("trigger_timeout_ms must be between 250 and 60000")
	}
	return nil
}

func (c *Config) normalize() {
	if strings.TrimSpace(c.ExtensionListen) == "" {
		c.ExtensionListen = defaultExtensionListen
	}
	if strings.TrimSpace(c.APIListen) == "" {
		c.APIListen = defaultAPIListen
	}
	// Legacy versions used a single public listen address. It is intentionally
	// ignored so upgrading cannot accidentally expose either endpoint publicly.
	c.LegacyListen = ""
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
