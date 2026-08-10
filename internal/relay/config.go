package relay

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	defaultListen           = "0.0.0.0:8787"
	defaultTriggerTimeoutMS = 5000
)

type Config struct {
	Listen           string `json:"listen"`
	Token            string `json:"token"`
	TriggerTimeoutMS int    `json:"trigger_timeout_ms"`
}

func DefaultConfig() (Config, error) {
	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		return Config{}, fmt.Errorf("generate token: %w", err)
	}
	return Config{
		Listen:           defaultListen,
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
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (c Config) Validate() error {
	if strings.TrimSpace(c.Listen) == "" {
		return errors.New("listen must not be empty")
	}
	if len(c.Token) < 32 {
		return errors.New("token must contain at least 32 characters")
	}
	if c.TriggerTimeoutMS < 250 || c.TriggerTimeoutMS > 60000 {
		return errors.New("trigger_timeout_ms must be between 250 and 60000")
	}
	return nil
}
