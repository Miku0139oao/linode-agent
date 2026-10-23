package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadFromEnv(t *testing.T) {
	t.Setenv("LINODE_TOKEN", "env-token")
	t.Setenv("LINODE_PROFILE", "")
	t.Setenv("LINODE_API_URL", "http://example.test/v4")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Token != "env-token" || cfg.TokenFrom != "env" {
		t.Fatalf("cfg = %+v", cfg)
	}
	if cfg.Profile != DefaultProfile {
		t.Fatalf("profile = %q", cfg.Profile)
	}
	if cfg.BaseURL != "http://example.test/v4" {
		t.Fatalf("base url = %q", cfg.BaseURL)
	}
}

func TestLoadFromFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LINODE_TOKEN", "")
	t.Setenv("LINODE_API_URL", "")
	t.Setenv("XDG_CONFIG_HOME", dir)

	path := filepath.Join(dir, "linode-agent", "credentials")
	if err := SaveToken(path, "work", "file-token"); err != nil {
		t.Fatalf("SaveToken: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("perm = %o, want 600", perm)
	}

	cfg, err := Load("work")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Token != "file-token" || cfg.TokenFrom != "file" {
		t.Fatalf("cfg = %+v", cfg)
	}
}

func TestLoadMissingToken(t *testing.T) {
	t.Setenv("LINODE_TOKEN", "")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if _, err := Load(""); err == nil {
		t.Fatal("expected ErrNoToken")
	}
}

func TestSaveTokenKeepsOtherProfiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "credentials")
	if err := SaveToken(path, "a", "token-a"); err != nil {
		t.Fatal(err)
	}
	if err := SaveToken(path, "b", "token-b"); err != nil {
		t.Fatal(err)
	}
	gotA, err := tokenFromFile(path, "a")
	if err != nil || gotA != "token-a" {
		t.Fatalf("profile a = %q, err %v", gotA, err)
	}
	gotB, err := tokenFromFile(path, "b")
	if err != nil || gotB != "token-b" {
		t.Fatalf("profile b = %q, err %v", gotB, err)
	}
}
