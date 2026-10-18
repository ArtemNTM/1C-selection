package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type httpStatusError struct {
	status  int
	message string
}

func (e *httpStatusError) Error() string { return e.message }

func (a *app) authenticatedRequest(ctx context.Context, method, path string, input, output any) error {
	if _, err := a.connect(ctx); err != nil {
		return err
	}
	err := a.request(ctx, method, path, a.sessionToken, input, output)
	var statusError *httpStatusError
	if !errors.As(err, &statusError) || statusError.status != http.StatusUnauthorized {
		return err
	}
	// A rejected session has not performed the operation. Renew once and repeat
	// the same payload, preserving note request IDs and notification ACK IDs.
	a.sessionToken, a.expiresAt = "", time.Time{}
	if _, err := a.connect(ctx); err != nil {
		return err
	}
	return a.request(ctx, method, path, a.sessionToken, input, output)
}

func normalizeServer(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || u == nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		return "", errors.New("--server must be an absolute http:// or https:// URL")
	}
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", errors.New("--server must not contain credentials, a query, or a fragment")
	}
	return strings.TrimRight(u.String(), "/"), nil
}

func (a *app) request(ctx context.Context, method, path, token string, input, output any) error {
	var body io.Reader
	if input != nil {
		data, err := json.Marshal(input)
		if err != nil {
			return err
		}
		body = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, a.server+path, body)
	if err != nil {
		return err
	}
	if input != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := a.http.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()
	const maxResponseSize = 8 << 20
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseSize+1))
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}
	if len(data) > maxResponseSize {
		return errors.New("server response is too large")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var envelope struct {
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal(data, &envelope) == nil && envelope.Error.Message != "" {
			return &httpStatusError{status: resp.StatusCode, message: fmt.Sprintf("%s: %s", envelope.Error.Code, envelope.Error.Message)}
		}
		return &httpStatusError{status: resp.StatusCode, message: fmt.Sprintf("server returned HTTP %d", resp.StatusCode)}
	}
	if output != nil {
		if err := json.Unmarshal(data, output); err != nil {
			return fmt.Errorf("decode server response: %w", err)
		}
	}
	return nil
}
