package config

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ErrNoToken is returned when no credential could be resolved.
var ErrNoToken = errors.New("no Linode API token found: set LINODE_TOKEN or run `linode-agent auth login`")

// Config is the resolved runtime configuration.
type Config struct {
	Profile   string
	Token     string
	BaseURL   string
	CredsPath string
	TokenFrom string // "env" | "file"
}

// DefaultProfile is used when LINODE_PROFILE is unset.
const DefaultProfile = "default"

// CredsPath returns ~/.config/linode-agent/credentials (XDG aware).
func CredsPath() string {
	dir := os.Getenv("XDG_CONFIG_HOME")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "credentials"
		}
		dir = filepath.Join(home, ".config")
	}
	return filepath.Join(dir, "linode-agent", "credentials")
}

// Load resolves configuration for the given profile ("" = LINODE_PROFILE or "default").
// Precedence: flags/env > credentials file.
func Load(profile string) (*Config, error) {
	if profile == "" {
		profile = os.Getenv("LINODE_PROFILE")
	}
	if profile == "" {
		profile = DefaultProfile
	}
	cfg := &Config{Profile: profile, CredsPath: CredsPath()}
	if v := os.Getenv("LINODE_API_URL"); v != "" {
		cfg.BaseURL = v
	}

	if tok := strings.TrimSpace(os.Getenv("LINODE_TOKEN")); tok != "" {
		cfg.Token = tok
		cfg.TokenFrom = "env"
		return cfg, nil
	}

	tok, err := tokenFromFile(cfg.CredsPath, profile)
	if err != nil {
		return nil, err
	}
	if tok != "" {
		cfg.Token = tok
		cfg.TokenFrom = "file"
		return cfg, nil
	}
	return nil, ErrNoToken
}

// tokenFromFile parses "profile=token" lines, honouring # comments.
func tokenFromFile(path, profile string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("read credentials: %w", err)
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		if strings.TrimSpace(key) == profile {
			return strings.TrimSpace(val), nil
		}
	}
	if err := sc.Err(); err != nil {
		return "", fmt.Errorf("read credentials: %w", err)
	}
	return "", nil
}

// SaveToken writes the token for profile into the credentials file (mode 0600)
// and returns the path written.
func SaveToken(path, profile, token string) error {
	if profile == "" {
		profile = DefaultProfile
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}

	lines := map[string]string{}
	order := []string{}
	if f, err := os.Open(path); err == nil {
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			key, val, ok := strings.Cut(line, "=")
			if !ok {
				continue
			}
			key = strings.TrimSpace(key)
			if _, seen := lines[key]; !seen {
				order = append(order, key)
			}
			lines[key] = strings.TrimSpace(val)
		}
		f.Close()
	}
	if _, seen := lines[profile]; !seen {
		order = append(order, profile)
	}
	lines[profile] = token

	var b strings.Builder
	for _, k := range order {
		fmt.Fprintf(&b, "%s=%s\n", k, lines[k])
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(b.String()), 0o600); err != nil {
		return err
	}
	if err := os.Chmod(tmp, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
