package service_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/bochkarevart/1c-selection/internal/httpapi"
	"github.com/bochkarevart/1c-selection/internal/model"
)

func TestPostgresHTTPSessionLifecycle(t *testing.T) {
	f := newFixture(t, 1.1, 3)
	client := f.client(t, "tonny", .25)
	handler, err := httpapi.New(f.s)
	if err != nil {
		t.Fatal(err)
	}
	do := func(method, path, token string, body any, want int) *httptest.ResponseRecorder {
		t.Helper()
		data := ""
		if body != nil {
			encoded, err := json.Marshal(body)
			if err != nil {
				t.Fatal(err)
			}
			data = string(encoded)
		}
		req := httptest.NewRequest(method, path, strings.NewReader(data))
		req.Header.Set("Content-Type", "application/json")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		if response.Code != want {
			t.Fatalf("%s %s: status=%d want=%d body=%s", method, path, response.Code, want, response.Body.String())
		}
		return response
	}
	hello := func() model.HelloResponse {
		t.Helper()
		response := do("POST", "/hello", "", model.HelloRequest{ClientID: client.Client.Name, Token: client.Token}, http.StatusOK)
		var result model.HelloResponse
		if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if result.Client.ID != client.Client.ID || result.SessionToken == "" || result.SessionToken == client.Token {
			t.Fatal("handshake did not return a separate session")
		}
		return result
	}
	note := f.note("request-before-handshake", "Bridge")
	do("POST", "/notes", "", note, http.StatusUnauthorized)
	do("POST", "/notes", client.Token, note, http.StatusUnauthorized)
	do("POST", "/hello", "", model.HelloRequest{ClientID: "unknown", Token: client.Token}, http.StatusUnauthorized)
	if sessionCount(t, f) != 0 || stats(t, f, client.Client.ID).NotesAccepted != 0 {
		t.Fatal("unauthenticated requests wrote a session or note")
	}
	first := hello()
	do("POST", "/notes", client.Token, note, http.StatusUnauthorized)
	do("POST", "/hello", "", model.HelloRequest{ClientID: client.Client.Name, Token: first.SessionToken}, http.StatusUnauthorized)
	do("POST", "/notes", first.SessionToken, note, http.StatusOK)
	do("GET", "/stats", first.SessionToken, nil, http.StatusForbidden)
	if _, err := f.db.Exec(context.Background(), "UPDATE clients SET role='editor' WHERE id=$1", client.Client.ID); err != nil {
		t.Fatal(err)
	}
	do("GET", "/stats", first.SessionToken, nil, http.StatusOK)
	if _, err := f.db.Exec(context.Background(), "UPDATE clients SET role='reporter' WHERE id=$1", client.Client.ID); err != nil {
		t.Fatal(err)
	}
	do("GET", "/stats", first.SessionToken, nil, http.StatusForbidden)
	f.now = f.now.Add(time.Hour)
	second := hello()
	f.now = first.ExpiresAt
	do("POST", "/notes", first.SessionToken, note, http.StatusUnauthorized)
	response := do("POST", "/notes", second.SessionToken, note, http.StatusOK)
	var receipt model.Receipt
	if err := json.Unmarshal(response.Body.Bytes(), &receipt); err != nil || receipt.Status != "duplicate" {
		t.Fatalf("retry under new session lost idempotency: receipt=%+v error=%v", receipt, err)
	}
	enabled := false
	if _, err := f.s.UpdateClient(context.Background(), client.Client.ID, model.UpdateClientRequest{Enabled: &enabled}); err != nil {
		t.Fatal(err)
	}
	do("GET", "/stats/me", second.SessionToken, nil, http.StatusUnauthorized)
	do("POST", "/hello", "", model.HelloRequest{ClientID: client.Client.Name, Token: client.Token}, http.StatusUnauthorized)
	if stats(t, f, client.Client.ID).NotesAccepted != 1 {
		t.Fatal("session renewal or rejected requests changed accepted notes")
	}
}
