package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"tt-trigger/internal/relay"
)

var version = "dev"

func main() {
	configPath := flag.String("config", "config.json", "path to the JSON configuration file")
	apiListen := flag.String("api-listen", "", "additional trigger API listen address")
	initConfig := flag.Bool("init", false, "create a configuration file if it does not exist")
	keyList := flag.Bool("key-list", false, "list configured HMAC key IDs")
	keyAdd := flag.String("key-add", "", "create an HMAC key with this ID")
	keyRemove := flag.String("key-remove", "", "remove the HMAC key with this ID")
	rotateExtensionToken := flag.Bool("extension-token-rotate", false, "replace the extension connection token")
	showVersion := flag.Bool("version", false, "print the version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Println(version)
		return
	}

	if *initConfig {
		cfg, changed, err := relay.EnsureConfig(*configPath)
		if err != nil {
			log.Fatalf("initialize configuration: %v", err)
		}
		if changed {
			fmt.Printf("Created or migrated %s\n", *configPath)
			fmt.Printf("Extension token: %s\n", cfg.ExtensionToken)
			fmt.Printf("Default HMAC key ID: %s\n", cfg.HMACKeys[0].ID)
			fmt.Printf("HMAC credentials were written to %s and are not printed.\n", *configPath)
		} else {
			fmt.Printf("Configuration already exists: %s\n", *configPath)
		}
		return
	}
	if *rotateExtensionToken {
		cfg, err := relay.LoadConfig(*configPath)
		if err != nil {
			log.Fatalf("load configuration: %v", err)
		}
		token, err := relay.GenerateExtensionToken()
		if err != nil {
			log.Fatalf("generate extension token: %v", err)
		}
		cfg.ExtensionToken = token
		if err := relay.SaveConfig(*configPath, cfg); err != nil {
			log.Fatalf("save configuration: %v", err)
		}
		fmt.Printf("Extension token: %s\n", token)
		return
	}

	if *keyList || *keyAdd != "" || *keyRemove != "" {
		manageKeys(*configPath, *keyList, *keyAdd, *keyRemove)
		return
	}

	cfg, err := relay.LoadConfig(*configPath)
	if err != nil {
		log.Fatalf("load configuration: %v", err)
	}
	if !relay.ExtensionTokenIsStrong(cfg.ExtensionToken) {
		log.Printf("WARNING: legacy extension_token format is accepted for compatibility; rotate it with --extension-token-rotate")
	}
	if *apiListen != "" {
		cfg.AdditionalAPIListen = *apiListen
		if err := cfg.Validate(); err != nil {
			log.Fatalf("validate additional API listen address: %v", err)
		}
	}

	logger := log.New(os.Stdout, "", log.LstdFlags|log.LUTC)
	server := relay.NewServer(cfg, logger)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		errCh <- server.ListenAndServe()
	}()

	select {
	case err := <-errCh:
		if err != nil {
			logger.Fatalf("server stopped: %v", err)
		}
	case <-ctx.Done():
		logger.Printf("shutdown requested")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			logger.Printf("shutdown error: %v", err)
		}
	}
}

func manageKeys(configPath string, list bool, add, remove string) {
	cfg, err := relay.LoadConfig(configPath)
	if err != nil {
		log.Fatalf("load configuration: %v", err)
	}
	operations := 0
	if list {
		operations++
	}
	if add != "" {
		operations++
	}
	if remove != "" {
		operations++
	}
	if operations != 1 {
		log.Fatal("choose exactly one of --key-list, --key-add, or --key-remove")
	}
	if list {
		for _, key := range cfg.HMACKeys {
			fmt.Println(key.ID)
		}
		return
	}
	if add != "" {
		for _, key := range cfg.HMACKeys {
			if key.ID == add {
				log.Fatalf("HMAC key %q already exists", add)
			}
		}
		key, err := relay.GenerateHMACKey(add)
		if err != nil {
			log.Fatalf("generate HMAC key: %v", err)
		}
		cfg.HMACKeys = append(cfg.HMACKeys, key)
		if err := relay.SaveConfig(configPath, cfg); err != nil {
			log.Fatalf("save configuration: %v", err)
		}
		fmt.Printf("Key ID: %s\nSecret: %s\n", key.ID, key.Secret)
		return
	}
	if len(cfg.HMACKeys) == 1 {
		log.Fatal("cannot remove the last HMAC key")
	}
	filtered := cfg.HMACKeys[:0]
	found := false
	for _, key := range cfg.HMACKeys {
		if key.ID == remove {
			found = true
			continue
		}
		filtered = append(filtered, key)
	}
	if !found {
		log.Fatalf("HMAC key %q was not found", remove)
	}
	cfg.HMACKeys = filtered
	if err := relay.SaveConfig(configPath, cfg); err != nil {
		log.Fatalf("save configuration: %v", err)
	}
	fmt.Printf("Removed HMAC key: %s\n", remove)
}
