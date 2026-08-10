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
	apiListen := flag.String("api-listen", "", "override the trigger API listen address")
	initConfig := flag.Bool("init", false, "create a configuration file if it does not exist")
	showVersion := flag.Bool("version", false, "print the version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Println(version)
		return
	}

	if *initConfig {
		cfg, created, err := relay.EnsureConfig(*configPath)
		if err != nil {
			log.Fatalf("initialize configuration: %v", err)
		}
		if created {
			fmt.Printf("Created %s\n", *configPath)
			fmt.Printf("Webhook token: %s\n", cfg.Token)
		} else {
			fmt.Printf("Configuration already exists: %s\n", *configPath)
		}
		return
	}

	cfg, err := relay.LoadConfig(*configPath)
	if err != nil {
		log.Fatalf("load configuration: %v", err)
	}
	if *apiListen != "" {
		cfg.APIListen = *apiListen
		if err := cfg.Validate(); err != nil {
			log.Fatalf("validate API listen override: %v", err)
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
