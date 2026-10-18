package server

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSessionTTLConfiguration(t *testing.T) {
	for _, tc := range []struct {
		name    string
		yaml    string
		flags   []string
		want    time.Duration
		invalid bool
	}{
		{name: "default", yaml: "{}", want: 24 * time.Hour},
		{name: "yaml", yaml: "session_ttl: 48h", want: 48 * time.Hour},
		{name: "flag overrides yaml", yaml: "session_ttl: 48h", flags: []string{"--session-ttl", "36h"}, want: 36 * time.Hour},
		{name: "zero yaml", yaml: "session_ttl: 0s", invalid: true},
		{name: "negative yaml", yaml: "session_ttl: -1h", invalid: true},
		{name: "zero flag overrides yaml", yaml: "session_ttl: 48h", flags: []string{"--session-ttl=0"}, invalid: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "server.yaml")
			if err := os.WriteFile(path, []byte(tc.yaml), 0o600); err != nil {
				t.Fatal(err)
			}
			called := false
			cmd := NewCommand(func(_ context.Context, cfg Config) error {
				called = true
				if cfg.SessionTTL != tc.want || cfg.serviceConfig().SessionTTL != tc.want {
					t.Fatalf("session TTL = %v, want %v", cfg.SessionTTL, tc.want)
				}
				return nil
			})
			cmd.SetArgs(append([]string{"--config", path}, tc.flags...))
			err := cmd.Execute()
			if (err != nil) != tc.invalid || called == tc.invalid {
				t.Fatalf("error = %v, server called = %v, invalid = %v", err, called, tc.invalid)
			}
		})
	}
}
