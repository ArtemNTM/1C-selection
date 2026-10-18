package server

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bochkarevart/1c-selection/internal/model"
)

func TestSaveIssuedTokenIsPrivateAndDoesNotOverwrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials", "editor.token")
	if err := saveIssuedToken(path, "server-issued-token"); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("token file permissions: %v, %v", info, err)
	}
	if err := saveIssuedToken(path, "replacement"); err == nil {
		t.Fatal("existing token was overwritten")
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "server-issued-token\n" {
		t.Fatalf("saved token changed: %v", err)
	}
}

func TestProvisionRejectsInvalidSavedTokenBeforeDatabaseAccess(t *testing.T) {
	for _, token := range []string{"", "\n", "too-short"} {
		t.Run(token, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "editor.token")
			if err := os.WriteFile(path, []byte(token), 0o600); err != nil {
				t.Fatal(err)
			}
			requests := []model.ProvisionClientRequest{{
				CreateClientRequest: model.CreateClientRequest{Name: "editor", Kind: "staff", Role: "editor", Trust: 1},
				TokenFile:           path,
			}}
			if err := provisionClients(context.Background(), nil, requests, io.Discard); err == nil {
				t.Fatal("invalid saved token was accepted")
			}
			if requests[0].Token != "" {
				t.Fatal("provisioning mutated the caller's config")
			}
		})
	}
}

func TestProvisionConfigAcceptsServerIssuedTokensAndRejectsSharedFiles(t *testing.T) {
	cfg := defaultConfig()
	cfg.Clients = []model.ProvisionClientRequest{{
		CreateClientRequest: model.CreateClientRequest{Name: "editor", Kind: "staff", Role: "editor", Trust: 1},
		TokenFile:           "credentials/editor.token",
	}}
	if err := cfg.validate(); err != nil {
		t.Fatalf("config without a pre-generated token rejected: %v", err)
	}
	cfg.Clients[0].TokenFile = ""
	if err := cfg.validate(); err == nil {
		t.Fatal("server-issued token without a persistent destination accepted")
	}
	cfg.Clients[0].TokenFile = "credentials/editor.token"
	cfg.Clients[0].Token = strings.Repeat("x", 32)
	if err := cfg.validate(); err == nil {
		t.Fatal("ambiguous token and token_file accepted")
	}
	cfg.Clients[0].Token = ""
	other := cfg.Clients[0]
	other.Name = "other"
	other.TokenFile = "./credentials/editor.token"
	cfg.Clients = append(cfg.Clients, other)
	if err := cfg.validate(); err == nil || !strings.Contains(err.Error(), "separate token_file") {
		t.Fatalf("shared credential path accepted: %v", err)
	}
}
