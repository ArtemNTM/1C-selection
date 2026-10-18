package service_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"testing"
	"time"

	"github.com/bochkarevart/1c-selection/internal/model"
	"github.com/bochkarevart/1c-selection/migrations"
)

func sessionCount(t *testing.T, f *fixture) int {
	t.Helper()
	var count int
	if err := f.db.QueryRow(context.Background(), "SELECT count(*) FROM sessions").Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func TestPostgresRejectedHandshakeCreatesNoSession(t *testing.T) {
	f := newFixture(t, 1.1, 3)
	client := f.client(t, "session-source", .5)
	ctx := context.Background()
	for _, credentials := range []struct{ name, token string }{
		{"unknown", client.Token},
		{client.Client.Name, "wrong-token"},
	} {
		response, err := f.s.CreateSession(ctx, credentials.name, credentials.token)
		errorCode(t, err, "unauthorized")
		if response != (model.HelloResponse{}) {
			t.Fatalf("rejected handshake returned credentials: %+v", response)
		}
	}
	enabled := false
	if _, err := f.s.UpdateClient(ctx, client.Client.ID, model.UpdateClientRequest{Enabled: &enabled}); err != nil {
		t.Fatal(err)
	}
	_, err := f.s.CreateSession(ctx, client.Client.Name, client.Token)
	errorCode(t, err, "unauthorized")
	if count := sessionCount(t, f); count != 0 {
		t.Fatalf("rejected handshakes created %d sessions", count)
	}
}

func TestPostgresSessionCredentialsAndExactExpiry(t *testing.T) {
	f := newFixture(t, 1.1, 3)
	client := f.client(t, "session-source", .5)
	ctx := context.Background()
	_, err := f.s.AuthenticateSession(ctx, client.Token)
	errorCode(t, err, "unauthorized")
	first, err := f.s.CreateSession(ctx, client.Client.Name, client.Token)
	if err != nil {
		t.Fatal(err)
	}
	if first.Client.ID != client.Client.ID || first.SessionToken == client.Token || !first.ExpiresAt.Equal(f.now.Add(f.cfg.SessionTTL)) {
		t.Fatalf("unexpected handshake: %+v", first)
	}
	random, err := base64.RawURLEncoding.DecodeString(first.SessionToken)
	if err != nil || len(random) != 32 {
		t.Fatalf("session token is not 32 random bytes: %v", err)
	}
	hash := sha256.Sum256([]byte(first.SessionToken))
	var stored []byte
	var storedClientID int64
	var createdAt, expiresAt time.Time
	if err := f.db.QueryRow(ctx, "SELECT token_hash, client_id, created_at, expires_at FROM sessions WHERE token_hash=$1", hash[:]).
		Scan(&stored, &storedClientID, &createdAt, &expiresAt); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(stored, hash[:]) || bytes.Equal(stored, []byte(first.SessionToken)) || storedClientID != client.Client.ID ||
		!createdAt.Equal(f.now) || !expiresAt.Equal(first.ExpiresAt) {
		t.Fatal("session metadata or token hash was not stored correctly")
	}
	_, err = f.s.Authenticate(ctx, client.Client.Name, first.SessionToken)
	errorCode(t, err, "unauthorized")
	_, err = f.s.AuthenticateSession(ctx, "unknown-session")
	errorCode(t, err, "unauthorized")
	f.now = f.now.Add(time.Hour)
	second, err := f.s.CreateSession(ctx, client.Client.Name, client.Token)
	if err != nil {
		t.Fatal(err)
	}
	if first.SessionToken == second.SessionToken || sessionCount(t, f) != 2 {
		t.Fatal("new handshake replaced the existing session")
	}
	for _, token := range []string{first.SessionToken, second.SessionToken} {
		if got, err := f.s.AuthenticateSession(ctx, token); err != nil || got.ID != client.Client.ID {
			t.Fatalf("independent session rejected: client=%+v err=%v", got, err)
		}
	}
	f.now = first.ExpiresAt.Add(-time.Microsecond)
	if _, err := f.s.AuthenticateSession(ctx, first.SessionToken); err != nil {
		t.Fatalf("session expired too early: %v", err)
	}
	f.now = first.ExpiresAt
	_, err = f.s.AuthenticateSession(ctx, first.SessionToken)
	errorCode(t, err, "unauthorized")
	if _, err := f.s.AuthenticateSession(ctx, second.SessionToken); err != nil {
		t.Fatalf("first expiry invalidated another session: %v", err)
	}
}

func TestPostgresSessionPermissionsAndPersistence(t *testing.T) {
	f := newFixture(t, 1.1, 3)
	client := f.client(t, "session-source", .5)
	ctx := context.Background()
	session, err := f.s.CreateSession(ctx, client.Client.Name, client.Token)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(ctx, "UPDATE clients SET role='editor' WHERE id=$1", client.Client.ID); err != nil {
		t.Fatal(err)
	}
	current, err := f.s.AuthenticateSession(ctx, session.SessionToken)
	if err != nil || current.Role != "editor" {
		t.Fatalf("session retained stale role: client=%+v err=%v", current, err)
	}
	enabled := false
	if _, err := f.s.UpdateClient(ctx, client.Client.ID, model.UpdateClientRequest{Enabled: &enabled}); err != nil {
		t.Fatal(err)
	}
	_, err = f.s.AuthenticateSession(ctx, session.SessionToken)
	errorCode(t, err, "unauthorized")
	enabled = true
	if _, err := f.s.UpdateClient(ctx, client.Client.ID, model.UpdateClientRequest{Enabled: &enabled}); err != nil {
		t.Fatal(err)
	}
	f.db.Close()
	f.open(t)
	if err := migrations.Migrate(ctx, f.db); err != nil {
		t.Fatal(err)
	}
	current, err = f.s.AuthenticateSession(ctx, session.SessionToken)
	if err != nil || current.ID != client.Client.ID || current.Role != "editor" {
		t.Fatalf("session did not survive reopening and migration: client=%+v err=%v", current, err)
	}
}

func TestPostgresSessionCleanupOnlyRemovesExpired(t *testing.T) {
	f := newFixture(t, 1.1, 3)
	client := f.client(t, "session-source", .5)
	ctx := context.Background()
	first, err := f.s.CreateSession(ctx, client.Client.Name, client.Token)
	if err != nil {
		t.Fatal(err)
	}
	f.now = f.now.Add(time.Hour)
	second, err := f.s.CreateSession(ctx, client.Client.Name, client.Token)
	if err != nil {
		t.Fatal(err)
	}
	f.now = first.ExpiresAt.Add(-time.Microsecond)
	if err := f.s.DeleteExpiredSessions(ctx); err != nil {
		t.Fatal(err)
	}
	if count := sessionCount(t, f); count != 2 {
		t.Fatalf("cleanup removed an active session: remaining=%d", count)
	}
	f.now = first.ExpiresAt
	if err := f.s.DeleteExpiredSessions(ctx); err != nil {
		t.Fatal(err)
	}
	if count := sessionCount(t, f); count != 1 {
		t.Fatalf("expired session was not removed: remaining=%d", count)
	}
	if _, err := f.s.AuthenticateSession(ctx, second.SessionToken); err != nil {
		t.Fatalf("cleanup invalidated the active session: %v", err)
	}
	if err := f.s.DeleteExpiredSessions(ctx); err != nil {
		t.Fatalf("repeated cleanup failed: %v", err)
	}
}
