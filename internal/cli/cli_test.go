package cli

import (
	"bytes"
	"errors"
	"flag"
	"log/slog"
	"strings"
	"testing"
)

func envFromMap(m map[string]string) EnvLookup {
	return func(k string) (string, bool) {
		v, ok := m[k]
		return v, ok
	}
}

func TestParseRequiresUserAndPassword(t *testing.T) {
	_, err := Parse(nil, envFromMap(nil), &bytes.Buffer{})
	if err == nil {
		t.Fatal("expected error when KEYCLOAK_USER missing")
	}
	if !strings.Contains(err.Error(), "KEYCLOAK_USER") {
		t.Errorf("unexpected error: %v", err)
	}

	_, err = Parse(nil, envFromMap(map[string]string{"KEYCLOAK_USER": "admin"}), &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "KEYCLOAK_PASSWORD") {
		t.Errorf("expected KEYCLOAK_PASSWORD error, got %v", err)
	}
}

func TestParseDefaults(t *testing.T) {
	env := map[string]string{
		"KEYCLOAK_USER":     "admin",
		"KEYCLOAK_PASSWORD": "pw",
	}
	opts, err := Parse(nil, envFromMap(env), &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if opts.KeycloakURL != "http://localhost:8080" {
		t.Errorf("unexpected url: %q", opts.KeycloakURL)
	}
	if opts.ConfigPath != "./config.yaml" {
		t.Errorf("unexpected config path: %q", opts.ConfigPath)
	}
	if opts.LogLevel != "info" {
		t.Errorf("unexpected log level: %q", opts.LogLevel)
	}
	if opts.DryRun {
		t.Error("dry-run should default to false")
	}
}

func TestParseDryRunFlag(t *testing.T) {
	env := map[string]string{
		"KEYCLOAK_USER":     "admin",
		"KEYCLOAK_PASSWORD": "pw",
	}
	opts, err := Parse([]string{"--dry-run"}, envFromMap(env), &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if !opts.DryRun {
		t.Error("expected dry-run true")
	}
}

func TestParseConfigFlagOverridesEnv(t *testing.T) {
	env := map[string]string{
		"KEYCLOAK_USER":        "admin",
		"KEYCLOAK_PASSWORD":    "pw",
		"KEYCLOAK_CONFIG_PATH": "/from/env.yaml",
	}
	opts, err := Parse([]string{"--config", "/from/flag.yaml"}, envFromMap(env), &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if opts.ConfigPath != "/from/flag.yaml" {
		t.Errorf("flag should override env, got %q", opts.ConfigPath)
	}
}

func TestParseVersionShortCircuits(t *testing.T) {
	opts, err := Parse([]string{"--version"}, envFromMap(nil), &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if !opts.ShowVersion {
		t.Error("expected ShowVersion true")
	}
	if opts.Username != "" || opts.Password != "" {
		t.Error("version path must not require credentials")
	}
}

func TestParseHelpReturnsErrHelp(t *testing.T) {
	_, err := Parse([]string{"--help"}, envFromMap(nil), &bytes.Buffer{})
	if !errors.Is(err, flag.ErrHelp) {
		t.Fatalf("expected flag.ErrHelp, got %v", err)
	}
}

func TestLevelFor(t *testing.T) {
	cases := map[string]slog.Level{
		"debug":   slog.LevelDebug,
		"info":    slog.LevelInfo,
		"warn":    slog.LevelWarn,
		"warning": slog.LevelWarn,
		"error":   slog.LevelError,
		"":        slog.LevelInfo,
		"weird":   slog.LevelInfo,
	}
	for in, want := range cases {
		if got := LevelFor(in); got != want {
			t.Errorf("LevelFor(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestParseSkipVersionCheck(t *testing.T) {
	lookup := envFromMap(map[string]string{
		"KEYCLOAK_USER":     "admin",
		"KEYCLOAK_PASSWORD": "secret",
	})

	opts, err := Parse([]string{"--skip-version-check"}, lookup, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if !opts.SkipVersionCheck {
		t.Error("expected SkipVersionCheck to be set")
	}

	opts, err = Parse(nil, lookup, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if opts.SkipVersionCheck {
		t.Error("SkipVersionCheck should default to false")
	}
}
