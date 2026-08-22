package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"keycloak-provisioner/internal/cli"
	"keycloak-provisioner/internal/client"
	"keycloak-provisioner/internal/compat"
	"keycloak-provisioner/internal/config"
	"keycloak-provisioner/internal/provisioner"
)

var version = "dev"

func main() {
	opts, err := cli.Parse(os.Args[1:], cli.OSLookup, os.Stderr)
	if err != nil {
		// flag already wrote usage to stderr; exit 0 for help, 2 for misuse.
		if errors.Is(err, flag.ErrHelp) {
			return
		}
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	if opts.ShowVersion {
		fmt.Println(version)
		return
	}

	cli.SetupLogging(os.Stdout, opts.LogLevel)
	slog.Info("Starting keycloak-provisioner", "version", version, "dryRun", opts.DryRun)

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	slog.Info("Loading configuration", "path", opts.ConfigPath)
	cfg, err := config.Load(opts.ConfigPath)
	if err != nil {
		slog.Error("Failed to load configuration", "error", err)
		os.Exit(1)
	}
	slog.Info("Configuration loaded", "realms", len(cfg.Realms))

	slog.Info("Connecting to Keycloak", "url", opts.KeycloakURL)
	kc := client.New(opts.KeycloakURL, opts.Username, opts.Password)
	if err := kc.Connect(ctx); err != nil {
		slog.Error("Failed to connect to Keycloak", "error", err)
		os.Exit(1)
	}

	// Refuse a config the server cannot apply before anything is mutated,
	// rather than failing partway through with a raw Keycloak error.
	if opts.SkipVersionCheck {
		slog.Warn("Skipping the Keycloak compatibility check (--skip-version-check)")
	} else if err := compat.Verify(ctx, kc, cfg); err != nil {
		slog.Error("Keycloak compatibility check failed", "error", err)
		os.Exit(1)
	}

	var api provisioner.KeycloakAPI = kc
	if opts.DryRun {
		slog.Info("Dry-run mode enabled — mutations will be logged but not applied")
		api = provisioner.NewDryRunAdapter(kc)
	}

	if err := provisioner.New(api, cfg).Run(ctx); err != nil {
		slog.Error("Provisioning failed", "error", err)
		os.Exit(1)
	}

	slog.Info("keycloak-provisioner finished successfully")
}
