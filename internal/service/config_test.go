package service

import (
	"testing"
	"time"
)

func TestSessionTTLConfiguration(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.SessionTTL != 24*time.Hour {
		t.Fatalf("default session TTL = %s", cfg.SessionTTL)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("default configuration is invalid: %v", err)
	}
	for _, ttl := range []time.Duration{0, -time.Second} {
		cfg.SessionTTL = ttl
		if err := cfg.Validate(); err == nil {
			t.Fatalf("invalid session TTL %s was accepted", ttl)
		}
	}
}
