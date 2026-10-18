package service_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"testing"

	"github.com/bochkarevart/1c-selection/internal/model"
	"github.com/bochkarevart/1c-selection/internal/service"
	"github.com/bochkarevart/1c-selection/migrations"
	"github.com/jackc/pgx/v5"
)

func TestPostgresProvisioningIssuesTokensAndPreservesExistingAccess(t *testing.T) {
	f := newFixture(t, 1.1, 3)
	ctx := context.Background()
	requests := []model.ProvisionClientRequest{
		{CreateClientRequest: model.CreateClientRequest{Name: "editor", Kind: "staff", Role: "editor", Trust: 1}},
		{CreateClientRequest: model.CreateClientRequest{Name: "reporter", Kind: "freelance", Role: "reporter", Trust: .25}},
	}
	var saved []model.ClientCredentials
	issued, err := service.ProvisionClients(ctx, f.db, requests, func(credentials model.ClientCredentials) error {
		saved = append(saved, credentials)
		return nil
	})
	if err != nil || len(issued) != 2 || len(saved) != 2 {
		t.Fatalf("issued=%d saved=%d error=%v", len(issued), len(saved), err)
	}
	for i, credentials := range issued {
		if credentials != saved[i] || credentials.Client.Name != requests[i].Name || credentials.Client.Role != requests[i].Role {
			t.Fatal("issued credentials do not match the requested client and saved token")
		}
		random, err := base64.RawURLEncoding.DecodeString(credentials.Token)
		if err != nil || len(random) != 32 {
			t.Fatal("server did not issue a token from 32 random bytes")
		}
		client, err := f.s.Authenticate(ctx, credentials.Client.Name, credentials.Token)
		if err != nil || client.ID != credentials.Client.ID {
			t.Fatalf("issued token rejected: %v", err)
		}
		var stored []byte
		if err := f.db.QueryRow(ctx, "SELECT token_hash FROM clients WHERE id=$1", client.ID).Scan(&stored); err != nil {
			t.Fatal(err)
		}
		hash := sha256.Sum256([]byte(credentials.Token))
		if !bytes.Equal(stored, hash[:]) {
			t.Fatal("database does not contain the issued token's hash")
		}
	}
	if issued[0].Token == issued[1].Token {
		t.Fatal("clients share a personal token")
	}
	trust, enabled := .6, false
	if _, err := f.s.UpdateClient(ctx, issued[1].Client.ID, model.UpdateClientRequest{Trust: &trust, Enabled: &enabled}); err != nil {
		t.Fatal(err)
	}
	reissued, err := service.ProvisionClients(ctx, f.db, requests, func(model.ClientCredentials) error {
		t.Fatal("restart attempted to save a replacement token")
		return nil
	})
	if err != nil || len(reissued) != 0 {
		t.Fatalf("restart issued %d credentials: %v", len(reissued), err)
	}
	if _, err := f.s.Authenticate(ctx, "editor", issued[0].Token); err != nil {
		t.Fatalf("restart invalidated the editor token: %v", err)
	}
	reporter, err := f.s.ClientByID(ctx, issued[1].Client.ID)
	if err != nil || reporter.Enabled || reporter.Trust != trust {
		t.Fatalf("restart overwrote client settings: %+v, %v", reporter, err)
	}
}

func TestPostgresProvisioningRollsBackWhenSavingCredentialsFails(t *testing.T) {
	f := newFixture(t, 1.1, 3)
	requests := []model.ProvisionClientRequest{
		{CreateClientRequest: model.CreateClientRequest{Name: "editor", Kind: "staff", Role: "editor", Trust: 1}},
		{CreateClientRequest: model.CreateClientRequest{Name: "reporter", Kind: "staff", Role: "reporter", Trust: 1}},
	}
	saveErr := errors.New("cannot write credential file")
	calls := 0
	issued, err := service.ProvisionClients(context.Background(), f.db, requests, func(model.ClientCredentials) error {
		calls++
		if calls == 2 {
			return saveErr
		}
		return nil
	})
	if !errors.Is(err, saveErr) || len(issued) != 0 || calls != 2 {
		t.Fatalf("issued=%d saves=%d error=%v", len(issued), calls, err)
	}
	var count int
	if err := f.db.QueryRow(context.Background(), "SELECT count(*) FROM clients").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("failed credential save left accounts in the database")
	}
}

func TestPostgresPersonalTokensAndProvisioning(t *testing.T) {
	f := newFixture(t, 1.1, 3)
	ctx := context.Background()
	provision := model.ProvisionClientRequest{
		CreateClientRequest: model.CreateClientRequest{
			Name:  "editor",
			Kind:  "staff",
			Role:  "editor",
			Trust: 1,
		},
		Token: "initial-editor-token-from-a-private-config",
	}
	if _, err := service.ProvisionClients(ctx, f.db, []model.ProvisionClientRequest{provision}, nil); err != nil {
		t.Fatal(err)
	}
	client, err := f.s.Authenticate(ctx, "editor", provision.Token)
	if err != nil || !client.Enabled || client.Role != "editor" {
		t.Fatalf("provisioned client: %+v, %v", client, err)
	}
	if _, err := f.s.Authenticate(ctx, "other", provision.Token); err == nil {
		t.Fatal("token accepted for another identity")
	}
	conn, err := pgx.Connect(ctx, f.url)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	var stored []byte
	if err := conn.QueryRow(ctx, "SELECT token_hash FROM clients WHERE id=$1", client.ID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	want := sha256.Sum256([]byte(provision.Token))
	if !bytes.Equal(stored, want[:]) || bytes.Equal(stored, []byte(provision.Token)) {
		t.Fatal("database does not contain the token hash")
	}
	trust, enabled := .6, false
	if _, err := f.s.UpdateClient(ctx, client.ID, model.UpdateClientRequest{
		Trust:   &trust,
		Enabled: &enabled,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ProvisionClients(ctx, f.db, []model.ProvisionClientRequest{provision}, nil); err != nil {
		t.Fatal(err)
	}
	if err := migrations.Migrate(ctx, f.db); err != nil {
		t.Fatal(err)
	}
	client, err = f.s.ClientByID(ctx, client.ID)
	if err != nil || client.Enabled || client.Trust != trust {
		t.Fatalf("restart replaced existing client settings: %+v, %v", client, err)
	}
	if _, err := f.s.Authenticate(ctx, client.Name, provision.Token); err == nil {
		t.Fatal("disabled client completed handshake")
	}
	enabled = true
	if _, err := f.s.UpdateClient(ctx, client.ID, model.UpdateClientRequest{Enabled: &enabled}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.Authenticate(ctx, client.Name, provision.Token); err != nil {
		t.Fatal(err)
	}
	created := f.client(t, "reporter", .25)
	random, err := base64.RawURLEncoding.DecodeString(created.Token)
	if err != nil || len(random) != 32 || created.Token == provision.Token {
		t.Fatal("client token was not generated from 32 random bytes")
	}
}

func TestPostgresMigrationPreservesExistingCredentials(t *testing.T) {
	f := newFixture(t, 1.1, 3)
	ctx := context.Background()
	client := f.client(t, "existing-reporter", .25)
	submit(t, f, client.Client.ID, f.note("existing-note", "Existing story"))
	conn, err := pgx.Connect(ctx, f.url)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	if _, err := conn.Exec(ctx, `ALTER TABLE clients DROP CONSTRAINT clients_role_check;
        UPDATE clients SET role='correspondent';
        ALTER TABLE clients ADD CONSTRAINT clients_role_check CHECK(role IN ('correspondent','editor'));
        ALTER TABLE clients RENAME COLUMN token_hash TO key_hash;
        ALTER TABLE clients DROP COLUMN enabled;`); err != nil {
		t.Fatal(err)
	}
	if err := migrations.Migrate(ctx, f.db); err != nil {
		t.Fatal(err)
	}
	got, err := f.s.Authenticate(ctx, client.Client.Name, client.Token)
	if err != nil || got.ID != client.Client.ID || got.Role != "reporter" || !got.Enabled {
		t.Fatalf("existing credentials lost: %+v, %v", got, err)
	}
	if got := stats(t, f, client.Client.ID); got.NotesAccepted != 1 {
		t.Fatalf("existing note lost: %+v", got)
	}
}
