package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/bochkarevart/1c-selection/internal/model"
	"github.com/bochkarevart/1c-selection/internal/service"
	"github.com/bochkarevart/1c-selection/internal/store"
)

func provisionClients(ctx context.Context, db store.Database, clients []model.ProvisionClientRequest, output io.Writer) error {
	requests := append([]model.ProvisionClientRequest(nil), clients...)
	paths := make(map[string]string, len(requests))
	for i := range requests {
		req := &requests[i]
		paths[req.Name] = req.TokenFile
		if req.Token != "" || req.TokenFile == "" {
			continue
		}
		// Reuse a token previously issued by this server, including after a
		// failed startup transaction. Never replace a saved credential.
		data, err := os.ReadFile(req.TokenFile)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return fmt.Errorf("read token for %q: %w", req.Name, err)
		}
		req.Token = strings.TrimSpace(string(data))
		if req.Token == "" {
			return fmt.Errorf("token file for %q is empty: %s", req.Name, req.TokenFile)
		}
	}
	issued, err := service.ProvisionClients(ctx, db, requests, func(credentials model.ClientCredentials) error {
		if path := paths[credentials.Client.Name]; path != "" {
			if err := saveIssuedToken(path, credentials.Token); err != nil {
				return fmt.Errorf("save token for %q: %w", credentials.Client.Name, err)
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	for _, credentials := range issued {
		if _, err := fmt.Fprintf(output, "Создан сотрудник %q, роль: %s.\nПерсональный токен: %s\n",
			credentials.Client.Name, credentials.Client.Role, credentials.Token); err != nil {
			return err
		}
		if path := paths[credentials.Client.Name]; path != "" {
			if _, err := fmt.Fprintf(output, "Токен сохранён в %s\n", path); err != nil {
				return err
			}
		}
	}
	return nil
}

func saveIssuedToken(path, token string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err = io.WriteString(file, token+"\n"); err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		_ = os.Remove(path)
	}
	return err
}
