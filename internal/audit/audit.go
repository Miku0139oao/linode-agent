package audit

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// Entry is one line in the audit log: every mutating request, applied or not.
type Entry struct {
	TS      string         `json:"ts"`
	Profile string         `json:"profile,omitempty"`
	Action  string         `json:"action"`
	Target  string         `json:"target"`
	DryRun  bool           `json:"dry_run"`
	OK      bool           `json:"ok"`
	Error   string         `json:"error,omitempty"`
	Request map[string]any `json:"request,omitempty"`
}

// Path returns ~/.local/state/linode-agent/audit.jsonl (XDG aware).
func Path() string {
	dir := os.Getenv("XDG_STATE_HOME")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "audit.jsonl"
		}
		dir = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(dir, "linode-agent", "audit.jsonl")
}

// Write appends one entry (mode 0600, created on demand).
func Write(e Entry) error {
	if e.TS == "" {
		e.TS = time.Now().UTC().Format(time.RFC3339)
	}
	path := Path()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	b, err := json.Marshal(e)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	_, err = f.Write(b)
	return err
}
