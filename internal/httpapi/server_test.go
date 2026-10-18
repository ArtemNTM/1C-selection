package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/bochkarevart/1c-selection/internal/model"
)

const testToken = "test-personal-token-for-the-current-client"
const testSessionToken = "test-session-token-for-the-current-client"

var testNow = time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)

type fakeService struct {
	Service
	client        model.Client
	submissions   int
	lastNote      model.NoteRequest
	authErr       error
	sessionIssued bool
	now           time.Time
	expiresAt     time.Time
}

func (db *fakeService) CreateSession(_ context.Context, name, key string) (model.HelloResponse, error) {
	if db.authErr != nil {
		return model.HelloResponse{}, db.authErr
	}
	if name != db.client.Name || key != testToken || !db.client.Enabled {
		return model.HelloResponse{}, model.NewError("unauthorized", "invalid credentials")
	}
	db.sessionIssued = true
	db.expiresAt = testNow.Add(24 * time.Hour)
	return model.HelloResponse{Client: db.client, SessionToken: testSessionToken, ExpiresAt: db.expiresAt}, nil
}

func (db *fakeService) AuthenticateSession(_ context.Context, token string) (model.Client, error) {
	if db.authErr != nil {
		return model.Client{}, db.authErr
	}
	if !db.sessionIssued || token != testSessionToken || !db.client.Enabled || !db.now.Before(db.expiresAt) {
		return model.Client{}, model.NewError("unauthorized", "invalid or expired session")
	}
	return db.client, nil
}

func (db *fakeService) ClientByID(_ context.Context, id int64) (model.Client, error) {
	if id != db.client.ID {
		return model.Client{}, model.NewError("not_found", "client not found")
	}
	return db.client, nil
}

func (db *fakeService) SubmitNote(_ context.Context, clientID int64, req model.NoteRequest) (model.Receipt, error) {
	if clientID != db.client.ID {
		panic("incorrect client ID")
	}
	db.submissions++
	db.lastNote = req
	return model.Receipt{
		Status:      "accepted",
		NoteID:      1,
		StoryID:     2,
		Contributes: true,
	}, nil
}

func (db *fakeService) AllStats(context.Context) ([]model.Stats, error) {
	return []model.Stats{{Client: db.client}}, nil
}

func (db *fakeService) Stats(context.Context, int64) (model.Stats, error) {
	return model.Stats{Client: db.client}, nil
}

func testHandler(t *testing.T) (*fakeService, http.Handler, string) {
	t.Helper()
	db := &fakeService{client: model.Client{
		ID:      7,
		Name:    "tonny",
		Role:    "reporter",
		Kind:    "freelance",
		Trust:   .25,
		Enabled: true,
	}}
	handler, err := newHandler(db)
	if err != nil {
		t.Fatal(err)
	}
	return db, handler, helloSession(t, handler, db.client.Name)
}

func helloSession(t *testing.T, handler http.Handler, name string) string {
	t.Helper()
	data, err := json.Marshal(model.HelloRequest{ClientID: name, Token: testToken})
	if err != nil {
		t.Fatal(err)
	}
	w := request(handler, http.MethodPost, "/hello", string(data), "")
	if w.Code != http.StatusOK {
		t.Fatalf("hello: %d %s", w.Code, w.Body.String())
	}
	var response model.HelloResponse
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	return response.SessionToken
}

func request(handler http.Handler, method, path, body, token string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	return w
}

func TestHelloChecksIdentityAndReturnsSession(t *testing.T) {
	db, handler, _ := testHandler(t)
	db.sessionIssued = false
	for _, token := range []string{testToken, testSessionToken} {
		if w := request(handler, http.MethodPost, "/notes", `{}`, token); w.Code != http.StatusUnauthorized {
			t.Fatalf("note accepted before hello: %d %s", w.Code, w.Body.String())
		}
	}
	data, _ := json.Marshal(model.HelloRequest{
		ClientID: "tonny",
		Token:    testToken,
	})
	w := request(handler, http.MethodPost, "/hello", string(data), "")
	if w.Code != http.StatusOK {
		t.Fatalf("hello: %d %s", w.Code, w.Body.String())
	}
	var response model.HelloResponse
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Client.ID != 7 || !response.Client.Enabled || strings.Contains(w.Body.String(), testToken) || response.SessionToken != testSessionToken || !response.ExpiresAt.Equal(testNow.Add(24*time.Hour)) {
		t.Fatalf("unexpected hello response: %s", w.Body.String())
	}
	if w := request(handler, http.MethodGet, "/stats/me", "", response.SessionToken); w.Code != http.StatusOK {
		t.Fatalf("session token rejected: %d", w.Code)
	}
	if w := request(handler, http.MethodGet, "/stats/me", "", testToken); w.Code != http.StatusUnauthorized {
		t.Fatalf("personal token accepted instead of session: %d", w.Code)
	}
	if w := request(handler, http.MethodPost, "/hello", `{"client_id":"tonny","token":"`+testSessionToken+`"}`, ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("session token accepted as personal credentials: %d", w.Code)
	}
	for _, identity := range []string{"other", "unknown"} {
		data, _ := json.Marshal(model.HelloRequest{
			ClientID: identity,
			Token:    testToken,
		})
		if w := request(handler, http.MethodPost, "/hello", string(data), ""); w.Code != http.StatusUnauthorized {
			t.Fatalf("wrong identity accepted: %d", w.Code)
		}
	}
}

func TestRejectsInvalidTokensAndDisabledClients(t *testing.T) {
	db, handler, token := testHandler(t)
	for _, invalid := range []string{"", testToken, "incorrect-token", "two tokens", strings.Repeat("x", 513)} {
		w := request(handler, http.MethodGet, "/stats/me", "", invalid)
		if w.Code != http.StatusUnauthorized || w.Header().Get("WWW-Authenticate") != "Bearer" {
			t.Fatalf("invalid token accepted: %d", w.Code)
		}
	}
	db.client.Enabled = false
	if w := request(handler, http.MethodGet, "/stats/me", "", token); w.Code != http.StatusUnauthorized {
		t.Fatalf("disabled client authorized: %d", w.Code)
	}
	data, _ := json.Marshal(model.HelloRequest{
		ClientID: db.client.Name,
		Token:    testToken,
	})
	if w := request(handler, http.MethodPost, "/hello", string(data), ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("disabled client completed handshake: %d", w.Code)
	}
}

func TestExpiredSessionDoesNotReachNoteHandler(t *testing.T) {
	db, handler, token := testHandler(t)
	db.now = db.expiresAt
	w := request(handler, http.MethodPost, "/notes", `{}`, token)
	if w.Code != http.StatusUnauthorized || db.submissions != 0 {
		t.Fatalf("expired session: status=%d submissions=%d", w.Code, db.submissions)
	}
}

func TestAllWorkingRoutesRejectPersonalTokens(t *testing.T) {
	_, handler, _ := testHandler(t)
	for _, route := range []struct{ method, path string }{
		{"POST", "/notes"}, {"GET", "/notifications"}, {"POST", "/notifications/1/ack"},
		{"GET", "/stats/me"}, {"GET", "/stats"}, {"GET", "/stories"}, {"GET", "/stories/1"},
		{"GET", "/clients"}, {"POST", "/clients"}, {"PATCH", "/clients/1"},
	} {
		w := request(handler, route.method, route.path, "", testToken)
		if w.Code != http.StatusUnauthorized {
			t.Errorf("%s %s accepted personal token: %d", route.method, route.path, w.Code)
		}
	}
}

func TestEditorPermissionUsesCurrentDatabaseRole(t *testing.T) {
	db, handler, token := testHandler(t)
	if w := request(handler, http.MethodGet, "/stats", "", token); w.Code != http.StatusForbidden {
		t.Fatalf("reporter could read global statistics: %d", w.Code)
	}
	db.client.Role = "editor"
	if w := request(handler, http.MethodGet, "/stats", "", token); w.Code != http.StatusOK {
		t.Fatalf("updated editor role not used: %d %s", w.Code, w.Body.String())
	}
	db.client.Role = "reporter"
	if w := request(handler, http.MethodGet, "/stats", "", token); w.Code != http.StatusForbidden {
		t.Fatalf("revoked editor permission still accepted: %d", w.Code)
	}
}

func TestStrictBoundedJSON(t *testing.T) {
	db, handler, token := testHandler(t)
	for _, body := range []string{
		`{"request_id":"one","unknown":1}`,
		`{"request_id":"one"} {"request_id":"two"}`,
		`{"request_id":"one"} trailing`,
		`{"request_id":`,
		``,
	} {
		w := request(handler, http.MethodPost, "/notes", body, token)
		if w.Code != http.StatusBadRequest {
			t.Errorf("body %q: status %d, response %s", body, w.Code, w.Body.String())
		}
	}
	tooLarge := `{"body":"` + strings.Repeat("x", maxRequestBytes) + `"}`
	if w := request(handler, http.MethodPost, "/notes", tooLarge, token); w.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("oversized request: status %d", w.Code)
	}
	if db.submissions != 0 {
		t.Errorf("invalid requests reached store: %d", db.submissions)
	}
}

func TestSubmitNotePassesAuthenticatedIdentityAndPayload(t *testing.T) {
	db, handler, token := testHandler(t)
	w := request(handler, http.MethodPost, "/notes", `{"request_id":"one","rubric":"City","topic":"Bridge","event_at":"2026-10-18T12:00:00Z"}`, token)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d, body %s", w.Code, w.Body.String())
	}
	if db.submissions != 1 || db.lastNote.RequestID != "one" || !db.lastNote.EventAt.Equal(testNow) {
		t.Fatalf("unexpected submitted note: %+v", db.lastNote)
	}
	var receipt model.Receipt
	if err := json.Unmarshal(w.Body.Bytes(), &receipt); err != nil {
		t.Fatal(err)
	}
	if receipt.Status != "accepted" || receipt.NoteID != 1 {
		t.Fatalf("unexpected receipt: %+v", receipt)
	}
}

func TestInvalidLimitsAndIDs(t *testing.T) {
	_, handler, token := testHandler(t)
	for _, path := range []string{
		"/stories?limit=0", "/stories?limit=1001", "/notifications?limit=bad",
		"/stories/not-an-id", "/stories/-1",
	} {
		if w := request(handler, http.MethodGet, path, "", token); w.Code != http.StatusBadRequest {
			t.Errorf("%s: status %d, body %s", path, w.Code, w.Body.String())
		}
	}
}

func TestInternalErrorsDoNotExposeDatabaseDetails(t *testing.T) {
	db, handler, _ := testHandler(t)
	db.authErr = errors.New("password=private database connection failed")
	w := request(handler, http.MethodPost, "/hello", `{"client_id":"tonny","token":"`+testToken+`"}`, "")
	if w.Code != http.StatusInternalServerError || strings.Contains(w.Body.String(), "private") {
		t.Fatalf("private error exposed: status %d, body %s", w.Code, w.Body.String())
	}
}

func TestRequiresService(t *testing.T) {
	if _, err := New(nil); err == nil {
		t.Fatal("nil database accepted")
	}
}
