package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/bochkarevart/1c-selection/internal/model"
	"github.com/spf13/cobra"
)

func runCLI(client *http.Client, dir, server string, output io.Writer, args ...string) error {
	cmd := NewCommand(client, func(_ *cobra.Command) (Config, error) {
		return Config{
			Server:         server,
			StateDir:       dir,
			RequestTimeout: 20 * time.Second,
			ClientID:       "tonny",
			Token:          "personal-token",
		}, nil
	})
	cmd.SetOut(output)
	cmd.SetErr(io.Discard)
	cmd.SetArgs(args)
	return cmd.Execute()
}

func helloResponse(w http.ResponseWriter, id int64) {
	_ = json.NewEncoder(w).Encode(model.HelloResponse{
		SessionToken: "session-token",
		ExpiresAt:    time.Now().Add(time.Hour),
		Client: model.Client{
			ID:      id,
			Name:    "tonny",
			Kind:    "freelance",
			Role:    "reporter",
			Trust:   0.25,
			Enabled: true,
		},
	})
}

func TestOutboxSurvivesLostReceiptAndNewProcess(t *testing.T) {
	dir := t.TempDir()
	var first model.NoteRequest
	var calls int
	var handlerErr error
	completed := make(chan struct{}, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/hello":
			helloResponse(w, 7)
		case "/notifications":
			_, _ = w.Write([]byte("[]"))
		case "/notes":
			defer func() {
				completed <- struct{}{}
			}()
			calls++
			var note model.NoteRequest
			if err := json.NewDecoder(r.Body).Decode(&note); err != nil {
				handlerErr = err
			}
			if r.Header.Get("Authorization") != "Bearer session-token" {
				handlerErr = errors.New("missing bearer token")
			}
			var onDisk pendingNote
			if err := readJSON(filepath.Join(dir, "outbox", note.RequestID+".json"), &onDisk); err != nil {
				handlerErr = fmt.Errorf("request sent before note persisted: %w", err)
			}
			if !reflect.DeepEqual(onDisk.Note, note) {
				handlerErr = errors.New("persisted note does not match request")
			}
			if calls == 1 {
				first = note
				conn, _, err := w.(http.Hijacker).Hijack()
				if err == nil {
					_ = conn.Close()
				} else {
					handlerErr = err
				}
				return
			}
			if !reflect.DeepEqual(first, note) {
				handlerErr = errors.New("retry changed the original request")
			}
			_ = json.NewEncoder(w).Encode(model.Receipt{
				Status:  "duplicate",
				NoteID:  20,
				StoryID: 3,
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	var out bytes.Buffer
	if err := runCLI(server.Client(), dir, server.URL, &out, "connect", "--no-ack"); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "secret") {
		t.Fatal("connect printed a secret")
	}
	err := runCLI(server.Client(), dir, server.URL, io.Discard, "note", "send", "--rubric", "Город", "--topic", "Мост", "--body", "Обрушился", "--event-at", "2026-10-18T12:00:00+03:00")
	if err == nil || !strings.Contains(err.Error(), "remains saved") {
		t.Fatalf("expected saved-note error, got %v", err)
	}
	<-completed
	if calls != 1 {
		t.Fatalf("unexpected first-send count: %d", calls)
	}
	if handlerErr != nil {
		t.Fatal(handlerErr)
	}
	for _, path := range []string{dir, filepath.Join(dir, "outbox")} {
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != 0o700 {
			t.Fatalf("private directory permissions: %s: %v", path, err)
		}
	}
	for _, path := range []string{filepath.Join(dir, "outbox", first.RequestID+".json")} {
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != 0o600 {
			t.Fatalf("private file permissions: %s: %v", path, err)
		}
	}
	if err := runCLI(server.Client(), dir, server.URL, io.Discard, "note", "retry", first.RequestID); err != nil {
		t.Fatal(err)
	}
	<-completed
	if handlerErr != nil {
		t.Fatal(handlerErr)
	}
	if calls != 2 {
		t.Fatalf("expected one retry, got %d calls", calls)
	}
	if _, err := os.Stat(filepath.Join(dir, "outbox", first.RequestID+".json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("confirmed note remains in outbox: %v", err)
	}
}

func TestPendingBoundToAccountAndServerAcrossCommands(t *testing.T) {
	dir := t.TempDir()
	clientID := int64(7)
	noteCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/hello") {
			helloResponse(w, clientID)
			return
		}
		if r.URL.Path == "/notifications" {
			_, _ = w.Write([]byte("[]"))
			return
		}
		noteCalls++
		_ = json.NewEncoder(w).Encode(model.Receipt{
			Status:  "accepted",
			NoteID:  1,
			StoryID: 1,
		})
	}))
	defer server.Close()
	connect := func() {
		t.Helper()
		if err := runCLI(server.Client(), dir, server.URL, io.Discard, "connect", "--no-ack"); err != nil {
			t.Fatal(err)
		}
	}
	connect()
	id, err := newRequestID()
	if err != nil {
		t.Fatal(err)
	}
	note := pendingNote{
		Server:   server.URL,
		ClientID: 7,
		SavedAt:  time.Now(),
		Note: model.NoteRequest{
			RequestID: id,
			Rubric:    "A",
			Topic:     "B",
			EventAt:   time.Now(),
		},
	}
	if err := atomicJSON(filepath.Join(dir, "outbox", id+".json"), note); err != nil {
		t.Fatal(err)
	}
	if err := runCLI(server.Client(), dir, server.URL+"/other-server", io.Discard, "note", "retry", id); err == nil || !strings.Contains(err.Error(), "another server") {
		t.Fatalf("expected identity/server rejection, got %v", err)
	}
	clientID = 8
	connect()
	if err := runCLI(server.Client(), dir, server.URL, io.Discard, "note", "retry", id); err == nil || !strings.Contains(err.Error(), "another server or account") {
		t.Fatalf("expected account rejection, got %v", err)
	}
	if err := runCLI(server.Client(), dir, server.URL, io.Discard, "note", "retry", "--all"); err != nil {
		t.Fatal(err)
	}
	if noteCalls != 0 {
		t.Fatalf("sent a note in the wrong account: %d", noteCalls)
	}
	clientID = 7
	connect()
	if err := runCLI(server.Client(), dir, server.URL, io.Discard, "note", "retry", id); err != nil {
		t.Fatal(err)
	}
	if noteCalls != 1 {
		t.Fatalf("expected original account retry after handshake: %d", noteCalls)
	}
}

func TestRetryAllContinuesAfterRejectedNote(t *testing.T) {
	dir := t.TempDir()
	const rejectedID = "00000000-0000-4000-8000-000000000001"
	const acceptedID = "00000000-0000-4000-8000-000000000002"
	var received []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/hello":
			helloResponse(w, 7)
		case "/notes":
			var note model.NoteRequest
			if err := json.NewDecoder(r.Body).Decode(&note); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			received = append(received, note.RequestID)
			if note.RequestID == rejectedID {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"error":{"code":"invalid","message":"topic is too long"}}`))
				return
			}
			_ = json.NewEncoder(w).Encode(model.Receipt{Status: "accepted", NoteID: 1, StoryID: 1})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	now := time.Now().UTC()
	for i, id := range []string{rejectedID, acceptedID} {
		note := pendingNote{
			Server: server.URL, ClientID: 7, SavedAt: now.Add(time.Duration(i) * time.Second),
			Note: model.NoteRequest{RequestID: id, Rubric: "Город", Topic: "Мост", EventAt: now},
		}
		if id == rejectedID {
			note.Note.Topic = strings.Repeat("x", 501)
		}
		if err := atomicJSON(filepath.Join(dir, "outbox", id+".json"), note); err != nil {
			t.Fatal(err)
		}
	}
	var output bytes.Buffer
	err := runCLI(server.Client(), dir, server.URL, &output, "note", "retry", "--all")
	if err == nil || !strings.Contains(err.Error(), "topic is too long") || !strings.Contains(err.Error(), rejectedID) {
		t.Fatalf("expected rejected-note error, got %v", err)
	}
	if !reflect.DeepEqual(received, []string{rejectedID, acceptedID}) {
		t.Fatalf("retried notes = %v, want rejected note followed by accepted note", received)
	}
	if !strings.Contains(output.String(), acceptedID) {
		t.Fatalf("successful receipt missing from output: %s", output.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "outbox", rejectedID+".json")); err != nil {
		t.Fatalf("rejected note must remain saved: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "outbox", acceptedID+".json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("accepted note must be removed from outbox: %v", err)
	}
}

type failingWriter struct{}

func (failingWriter) Write(_ []byte) (int, error) {
	return 0, errors.New("output unavailable")
}

func TestNotificationsAcknowledgedOnlyAfterSuccessfulOutput(t *testing.T) {
	dir := t.TempDir()
	acks := 0
	var out bytes.Buffer
	var ackBeforeOutput bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/hello":
			helloResponse(w, 7)
		case "/notifications":
			_ = json.NewEncoder(w).Encode([]model.Notification{{
				ID:       12,
				ClientID: 7,
				Message:  "Congratulations",
			}})
		case "/notifications/12/ack":
			acks++
			ackBeforeOutput = !strings.Contains(out.String(), "Congratulations")
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	if err := runCLI(server.Client(), dir, server.URL, io.Discard, "connect", "--no-ack"); err != nil {
		t.Fatal(err)
	}
	if err := runCLI(server.Client(), dir, server.URL, failingWriter{}, "notifications"); err == nil {
		t.Fatal("expected output failure")
	}
	if acks != 0 {
		t.Fatal("acknowledged notification despite failed output")
	}
	if err := runCLI(server.Client(), dir, server.URL, &out, "notifications", "--no-ack"); err != nil {
		t.Fatal(err)
	}
	if acks != 0 {
		t.Fatal("--no-ack acknowledged notifications")
	}
	out.Reset()
	if err := runCLI(server.Client(), dir, server.URL, &out, "notifications"); err != nil {
		t.Fatal(err)
	}
	if acks != 1 || ackBeforeOutput {
		t.Fatalf("acknowledgements=%d, before output=%t", acks, ackBeforeOutput)
	}
}

func TestRejectMalformedServerAndRequestIDs(t *testing.T) {
	for _, server := range []string{"localhost:8080", "ftp://localhost", "https://user:secret@example.com", "https://example.com?token=secret", "https://example.com#fragment"} {
		if _, err := normalizeServer(server); err == nil {
			t.Errorf("accepted malformed server %q", server)
		}
	}
	a := &app{stateDir: t.TempDir()}
	for _, id := range []string{"../profile", "", "not-a-uuid"} {
		if _, err := a.pendingPath(id); err == nil {
			t.Errorf("accepted malformed request ID %q", id)
		}
	}
}

func TestClientTrustDefaultsAndExplicitOverride(t *testing.T) {
	dir := t.TempDir()
	var received model.CreateClientRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/hello") {
			helloResponse(w, 7)
			return
		}
		if r.URL.Path == "/notifications" {
			_, _ = w.Write([]byte("[]"))
			return
		}
		if r.URL.Path != "/clients" || r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(w).Encode(model.ClientCredentials{
			Client: model.Client{ID: 8},
			Token:  "generated-token",
		})
	}))
	defer server.Close()
	if err := runCLI(server.Client(), dir, server.URL, io.Discard, "connect", "--no-ack"); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name  string
		args  []string
		trust float64
	}{
		{"staff", []string{"--kind", "staff"}, 1},
		{"freelance", []string{"--kind", "freelance"}, 0.25},
		{"explicit staff trust", []string{"--kind", "staff", "--trust", "0.4"}, 0.4},
	} {
		t.Run(test.name, func(t *testing.T) {
			args := append([]string{"clients", "create", "--name", "new"}, test.args...)
			if err := runCLI(server.Client(), dir, server.URL, io.Discard, args...); err != nil {
				t.Fatal(err)
			}
			if received.Trust != test.trust {
				t.Fatalf("trust = %v, expected %v", received.Trust, test.trust)
			}
		})
	}
}
