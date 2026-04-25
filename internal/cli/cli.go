// Package cli wires command-line flags, environment variables, and logging
// for the keycloak-provisioner binary. Extracted from main so it can be
// unit-tested.
package cli

import (
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
)

// Options is the resolved configuration for a single invocation.
type Options struct {
	KeycloakURL string
	Username    string
	Password    string
	ConfigPath  string
	LogLevel    string
	DryRun      bool
	ShowVersion bool
}

// EnvLookup is os.LookupEnv-shaped; injected for tests.
type EnvLookup func(string) (string, bool)

// Parse parses args (without program name) and resolves environment variables
// via lookup. Returns the resolved Options or a non-nil error. Help and
// version output are written to out.
//
// If --version is requested, ShowVersion is set and the caller should print
// version info and exit 0 without further work.
func Parse(args []string, lookup EnvLookup, out io.Writer) (*Options, error) {
	fs := flag.NewFlagSet("keycloak-provisioner", flag.ContinueOnError)
	fs.SetOutput(out)

	dryRun := fs.Bool("dry-run", false, "log intended changes without applying them")
	showVersion := fs.Bool("version", false, "print version and exit")
	configFlag := fs.String("config", "", "path to YAML config (overrides KEYCLOAK_CONFIG_PATH)")

	if err := fs.Parse(args); err != nil {
		return nil, err
	}

	opts := &Options{
		ShowVersion: *showVersion,
		DryRun:      *dryRun,
		KeycloakURL: envOrDefault(lookup, "KEYCLOAK_URL", "http://localhost:8080"),
		ConfigPath:  envOrDefault(lookup, "KEYCLOAK_CONFIG_PATH", "./config.yaml"),
		LogLevel:    envOrDefault(lookup, "LOG_LEVEL", "info"),
	}
	if *configFlag != "" {
		opts.ConfigPath = *configFlag
	}
	if opts.ShowVersion {
		return opts, nil
	}

	user, ok := lookup("KEYCLOAK_USER")
	if !ok || user == "" {
		return nil, fmt.Errorf("required environment variable KEYCLOAK_USER is not set")
	}
	pass, ok := lookup("KEYCLOAK_PASSWORD")
	if !ok || pass == "" {
		return nil, fmt.Errorf("required environment variable KEYCLOAK_PASSWORD is not set")
	}
	opts.Username = user
	opts.Password = pass

	return opts, nil
}

func envOrDefault(lookup EnvLookup, key, def string) string {
	if v, ok := lookup(key); ok && v != "" {
		return v
	}
	return def
}

// LevelFor returns the slog level for a textual name. Unknown names default to info.
func LevelFor(name string) slog.Level {
	switch name {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// SetupLogging installs the default structured JSON logger writing to w.
func SetupLogging(w io.Writer, level string) {
	slog.SetDefault(slog.New(slog.NewJSONHandler(w, &slog.HandlerOptions{Level: LevelFor(level)})))
}

// OSLookup is the production environment lookup.
func OSLookup(key string) (string, bool) { return os.LookupEnv(key) }
