package cli

import (
	"context"
	"encoding/json"
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

func TestCommandsUseSessionAfterPersonalHandshake(t *testing.T) {
	const requestID = "00000000-0000-4000-8000-000000000001"
	for _, test := range []struct {
		name, method, path, response string
		args                         []string
	}{
		{"connect", "GET", "/notifications", "[]", []string{"connect", "--no-ack"}},
		{"notifications", "GET", "/notifications", "[]", []string{"notifications", "--no-ack"}},
		{"stats", "GET", "/stats/me", "{}", []string{"stats"}},
		{"all stats", "GET", "/stats", "[]", []string{"stats", "--all"}},
		{"clients", "GET", "/clients", "[]", []string{"clients", "list"}},
		{"create client", "POST", "/clients", "{}", []string{"clients", "create", "--name", "new"}},
		{"trust", "PATCH", "/clients/8", "{}", []string{"clients", "trust", "8", "--trust", "0.5"}},
		{"enabled", "PATCH", "/clients/8", "{}", []string{"clients", "enabled", "8", "--enabled=false"}},
		{"stories", "GET", "/stories", "[]", []string{"stories"}},
		{"story", "GET", "/stories/9", "{}", []string{"stories", "9"}},
		{"send", "POST", "/notes", `{"status":"accepted"}`, []string{"note", "send", "--rubric", "City", "--topic", "Bridge"}},
		{"retry", "POST", "/notes", `{"status":"duplicate"}`, []string{"note", "retry", requestID}},
		{"retry all", "POST", "/notes", `{"status":"duplicate"}`, []string{"note", "retry", "--all"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			hellos, operations := 0, 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/hello" {
					hellos++
					var request model.HelloRequest
					if err := json.NewDecoder(r.Body).Decode(&request); err != nil || request.ClientID != "tonny" || request.Token != "personal-token" {
						t.Errorf("invalid personal handshake: %+v, %v", request, err)
					}
					if r.Header.Get("Authorization") != "" {
						t.Error("handshake sent a bearer token")
					}
					helloResponse(w, 7)
					return
				}
				operations++
				if hellos == 0 || r.Header.Get("Authorization") != "Bearer session-token" || r.Method != test.method || r.URL.Path != test.path {
					t.Errorf("unexpected working request: %s %s, authorization %q, hellos %d", r.Method, r.URL.Path, r.Header.Get("Authorization"), hellos)
				}
				_, _ = io.WriteString(w, test.response)
			}))
			defer server.Close()
			dir := t.TempDir()
			if test.name == "retry" || test.name == "retry all" {
				if err := atomicJSON(filepath.Join(dir, "outbox", requestID+".json"), pendingNote{
					Server: server.URL, ClientID: 7, SavedAt: time.Now(),
					Note: model.NoteRequest{RequestID: requestID, Rubric: "City", Topic: "Bridge", EventAt: time.Now()},
				}); err != nil {
					t.Fatal(err)
				}
			}
			if err := runCLI(server.Client(), dir, server.URL, io.Discard, test.args...); err != nil {
				t.Fatal(err)
			}
			if hellos != 1 || operations != 1 {
				t.Fatalf("hellos=%d operations=%d", hellos, operations)
			}
		})
	}
}

func sessionHello(w http.ResponseWriter, token string, expiresAt time.Time) {
	_ = json.NewEncoder(w).Encode(model.HelloResponse{
		Client:       model.Client{ID: 7, Name: "tonny", Enabled: true},
		SessionToken: token, ExpiresAt: expiresAt,
	})
}

func TestConnectRejectsInvalidSessions(t *testing.T) {
	for _, test := range []struct {
		name, token string
		expiresAt   time.Time
	}{
		{"missing token", "", time.Now().Add(time.Hour)},
		{"long token", strings.Repeat("x", 513), time.Now().Add(time.Hour)},
		{"space", "bad token", time.Now().Add(time.Hour)},
		{"control", "bad\ntoken", time.Now().Add(time.Hour)},
		{"expired", "session-token", time.Now().Add(-time.Hour)},
		{"missing expiry", "session-token", time.Time{}},
	} {
		t.Run(test.name, func(t *testing.T) {
			operations := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/hello" {
					sessionHello(w, test.token, test.expiresAt)
					return
				}
				operations++
			}))
			defer server.Close()
			if err := runCLI(server.Client(), t.TempDir(), server.URL, io.Discard, "stats"); err == nil || !strings.Contains(err.Error(), "invalid session") {
				t.Fatalf("expected invalid session, got %v", err)
			}
			if operations != 0 {
				t.Fatal("working request used invalid session")
			}
		})
	}
}

func TestSessionRenewalRepeatsExactNoteOnce(t *testing.T) {
	for _, test := range []struct {
		name           string
		firstStatus    int
		secondStatus   int
		wantHellos     int
		wantOperations int
		wantSuccess    bool
	}{
		{"expired session", http.StatusUnauthorized, http.StatusOK, 2, 2, true},
		{"renewed session rejected", http.StatusUnauthorized, http.StatusUnauthorized, 2, 2, false},
		{"forbidden", http.StatusForbidden, http.StatusOK, 1, 1, false},
		{"network failure", 0, http.StatusOK, 1, 1, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			hellos := 0
			var notes []model.NoteRequest
			operationsDone := make(chan struct{}, 2)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/hello" {
					hellos++
					sessionHello(w, fmt.Sprintf("session-%d", hellos), time.Now().Add(time.Hour))
					return
				}
				if r.Header.Get("Authorization") != fmt.Sprintf("Bearer session-%d", hellos) {
					t.Error("working request did not use latest session")
				}
				defer func() { operationsDone <- struct{}{} }()
				var note model.NoteRequest
				if err := json.NewDecoder(r.Body).Decode(&note); err != nil {
					t.Error(err)
				}
				notes = append(notes, note)
				status := test.firstStatus
				if len(notes) > 1 {
					status = test.secondStatus
				}
				if status == 0 {
					conn, _, err := w.(http.Hijacker).Hijack()
					if err != nil {
						t.Error(err)
						return
					}
					_ = conn.Close()
					return
				}
				w.WriteHeader(status)
				_, _ = io.WriteString(w, `{"status":"accepted"}`)
			}))
			defer server.Close()
			dir := t.TempDir()
			err := runCLI(server.Client(), dir, server.URL, io.Discard, "note", "send", "--rubric", "City", "--topic", "Bridge", "--body", "Original text")
			for i := 0; i < test.wantOperations; i++ {
				<-operationsDone
			}
			if (err == nil) != test.wantSuccess || hellos != test.wantHellos || len(notes) != test.wantOperations {
				t.Fatalf("error=%v hellos=%d operations=%d", err, hellos, len(notes))
			}
			if len(notes) == 2 && !reflect.DeepEqual(notes[0], notes[1]) {
				t.Fatalf("session renewal changed note: %+v vs %+v", notes[0], notes[1])
			}
			_, statErr := os.Stat(filepath.Join(dir, "outbox", notes[0].RequestID+".json"))
			if test.wantSuccess && !os.IsNotExist(statErr) || !test.wantSuccess && statErr != nil {
				t.Fatalf("unexpected pending state: %v", statErr)
			}
		})
	}
}

func TestAuthenticatedRequestRenewsLocallyExpiredSession(t *testing.T) {
	now := time.Now().UTC()
	hellos, operations := 0, 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/hello" {
			hellos++
			sessionHello(w, fmt.Sprintf("session-%d", hellos), now.Add(time.Minute))
			return
		}
		operations++
		if r.Header.Get("Authorization") != fmt.Sprintf("Bearer session-%d", operations) {
			t.Error("locally expired session was used")
		}
		_, _ = io.WriteString(w, "{}")
	}))
	defer server.Close()
	a := &app{server: server.URL, clientID: "tonny", token: "personal-token", http: server.Client(), now: func() time.Time { return now }}
	for i := 0; i < 2; i++ {
		if err := a.authenticatedRequest(context.Background(), http.MethodGet, "/stats/me", nil, nil); err != nil {
			t.Fatal(err)
		}
		now = now.Add(2 * time.Minute)
	}
	if hellos != 2 || operations != 2 {
		t.Fatalf("hellos=%d operations=%d", hellos, operations)
	}
}

func TestNotificationACKRenewsRejectedSession(t *testing.T) {
	hellos, acks := 0, 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/hello":
			hellos++
			sessionHello(w, fmt.Sprintf("session-%d", hellos), time.Now().Add(time.Hour))
		case "/notifications":
			_, _ = io.WriteString(w, `[{"id":12,"message":"Congratulations"}]`)
		case "/notifications/12/ack":
			acks++
			if r.Header.Get("Authorization") != fmt.Sprintf("Bearer session-%d", acks) {
				t.Error("ACK did not use expected session")
			}
			if acks == 1 {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	if err := runCLI(server.Client(), t.TempDir(), server.URL, io.Discard, "notifications"); err != nil {
		t.Fatal(err)
	}
	if hellos != 2 || acks != 2 {
		t.Fatalf("hellos=%d acks=%d", hellos, acks)
	}
}

func TestSessionRenewalRejectsChangedAccount(t *testing.T) {
	for _, localExpiry := range []bool{false, true} {
		t.Run(fmt.Sprintf("local_expiry=%t", localExpiry), func(t *testing.T) {
			hellos, operations := 0, 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/hello" {
					hellos++
					_ = json.NewEncoder(w).Encode(model.HelloResponse{
						Client:       model.Client{ID: 8, Name: "tonny", Enabled: true},
						SessionToken: "new-session", ExpiresAt: time.Now().Add(time.Hour),
					})
					return
				}
				operations++
				w.WriteHeader(http.StatusUnauthorized)
			}))
			defer server.Close()
			now := time.Now().UTC()
			a := &app{
				server: server.URL, stateDir: t.TempDir(), clientID: "tonny", token: "personal-token",
				client:       &model.Client{ID: 7, Name: "tonny", Enabled: true},
				sessionToken: "original-session", expiresAt: now.Add(time.Hour),
				http: server.Client(), now: time.Now,
			}
			if localExpiry {
				a.expiresAt = now.Add(-time.Hour)
			}
			const requestID = "00000000-0000-4000-8000-000000000001"
			note := pendingNote{
				Server: server.URL, ClientID: 7, SavedAt: now,
				Note: model.NoteRequest{RequestID: requestID, Rubric: "City", Topic: "Bridge", EventAt: now},
			}
			path, err := a.pendingPath(requestID)
			if err != nil {
				t.Fatal(err)
			}
			if err := atomicJSON(path, note); err != nil {
				t.Fatal(err)
			}
			cmd := &cobra.Command{}
			cmd.SetContext(context.Background())
			cmd.SetOut(io.Discard)
			err = a.sendPending(cmd, *a.client, note)
			if err == nil || !strings.Contains(err.Error(), "different client identity") {
				t.Fatalf("expected identity mismatch, got %v", err)
			}
			wantOperations := 1
			if localExpiry {
				wantOperations = 0
			}
			if hellos != 1 || operations != wantOperations || a.client.ID != 7 {
				t.Fatalf("hellos=%d operations=%d client=%+v", hellos, operations, a.client)
			}
			var saved pendingNote
			if err := readJSON(path, &saved); err != nil || !reflect.DeepEqual(saved, note) {
				t.Fatalf("original pending note was changed: %+v, %v", saved, err)
			}
		})
	}
}
