package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"sync"
	"testing"

	"github.com/bochkarevart/1c-selection/internal/model"
)

type captureService struct {
	*fakeService

	mu             sync.Mutex
	storyLimits    []int
	storyIDs       []int64
	updateIDs      []int64
	updateRequests []model.UpdateClientRequest
	noteRequests   []model.NoteRequest
	noteClientIDs  []int64
}

func newCaptureService() *captureService {
	return &captureService{fakeService: &fakeService{client: model.Client{
		ID:        7,
		Name:      "editor",
		Role:      "editor",
		Kind:      "staff",
		Trust:     .25,
		CreatedAt: testNow,
		Enabled:   true,
	}}}
}

func (db *captureService) Stories(_ context.Context, limit int) ([]model.Story, error) {
	db.mu.Lock()
	db.storyLimits = append(db.storyLimits, limit)
	db.mu.Unlock()
	return []model.Story{}, nil
}

func (db *captureService) Story(_ context.Context, id int64) (model.Story, error) {
	db.mu.Lock()
	db.storyIDs = append(db.storyIDs, id)
	db.mu.Unlock()
	return model.Story{ID: id}, nil
}

func (db *captureService) UpdateClient(_ context.Context, id int64, req model.UpdateClientRequest) (model.Client, error) {
	db.mu.Lock()
	db.updateIDs = append(db.updateIDs, id)
	db.updateRequests = append(db.updateRequests, req)
	db.mu.Unlock()
	return db.client, nil
}

func (db *captureService) SubmitNote(_ context.Context, clientID int64, req model.NoteRequest) (model.Receipt, error) {
	db.mu.Lock()
	db.noteClientIDs = append(db.noteClientIDs, clientID)
	db.noteRequests = append(db.noteRequests, req)
	db.mu.Unlock()
	return model.Receipt{
		Status:      "accepted",
		NoteID:      1,
		StoryID:     2,
		Contributes: true,
	}, nil
}

func captureHandler(t *testing.T) (*captureService, http.Handler, string) {
	t.Helper()
	db := newCaptureService()
	handler, err := newHandler(db)
	if err != nil {
		t.Fatal(err)
	}
	return db, handler, helloSession(t, handler, db.client.Name)
}

func TestRouteBindingPassesQueryAndPathParameters(t *testing.T) {
	db, handler, token := captureHandler(t)

	if w := request(handler, http.MethodGet, "/stories?limit=7", "", token); w.Code != http.StatusOK {
		t.Fatalf("list stories: status %d, body %s", w.Code, w.Body.String())
	}
	if w := request(handler, http.MethodGet, "/stories/42", "", token); w.Code != http.StatusOK {
		t.Fatalf("story: status %d, body %s", w.Code, w.Body.String())
	}

	db.mu.Lock()
	defer db.mu.Unlock()
	if got := db.storyLimits; len(got) != 1 || got[0] != 7 {
		t.Fatalf("Stories limits = %v, want [7]", got)
	}
	if got := db.storyIDs; len(got) != 1 || got[0] != 42 {
		t.Fatalf("Story IDs = %v, want [42]", got)
	}
}

func TestRouteBindingKeepsPathIDOutOfJSONBody(t *testing.T) {
	db, handler, token := captureHandler(t)

	if w := request(handler, http.MethodPatch, "/clients/11", `{"trust":0.8}`, token); w.Code != http.StatusOK {
		t.Fatalf("valid update: status %d, body %s", w.Code, w.Body.String())
	}
	db.mu.Lock()
	if len(db.updateIDs) != 1 || db.updateIDs[0] != 11 || db.updateRequests[0].Trust == nil || *db.updateRequests[0].Trust != .8 {
		t.Fatalf("UpdateClient calls = ids %v, requests %+v", db.updateIDs, db.updateRequests)
	}
	db.mu.Unlock()

	if w := request(handler, http.MethodPatch, "/clients/11", `{"id":999,"trust":0.8}`, token); w.Code != http.StatusBadRequest {
		t.Fatalf("body id: status %d, body %s", w.Code, w.Body.String())
	}
	db.mu.Lock()
	defer db.mu.Unlock()
	if len(db.updateIDs) != 1 {
		t.Fatalf("invalid JSON reached UpdateClient: calls %v", db.updateIDs)
	}
}

func TestRouteBindingValidatesTimeBeforeSubmitCallback(t *testing.T) {
	db, handler, token := captureHandler(t)

	if w := request(handler, http.MethodPost, "/notes", `{"request_id":"invalid-time","event_at":"not-rfc3339"}`, token); w.Code != http.StatusBadRequest {
		t.Fatalf("invalid event_at: status %d, body %s", w.Code, w.Body.String())
	}
	db.mu.Lock()
	if len(db.noteRequests) != 0 {
		t.Fatalf("invalid event_at reached SubmitNote: %+v", db.noteRequests)
	}
	db.mu.Unlock()

	if w := request(handler, http.MethodPost, "/notes", `{"request_id":"valid-time","event_at":"2026-10-18T12:00:00Z"}`, token); w.Code != http.StatusOK {
		t.Fatalf("valid event_at: status %d, body %s", w.Code, w.Body.String())
	}
	db.mu.Lock()
	defer db.mu.Unlock()
	if len(db.noteRequests) != 1 || !db.noteRequests[0].EventAt.Equal(testNow) {
		t.Fatalf("submitted notes = %+v, want decoded RFC3339 event_at", db.noteRequests)
	}
}

func TestRouteBindingAllocatesNoteParamsPerConcurrentRequest(t *testing.T) {
	db, handler, token := captureHandler(t)
	const requests = 32

	var wg sync.WaitGroup
	errs := make(chan error, requests)
	for i := 0; i < requests; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			body := fmt.Sprintf(`{"request_id":"request-%02d","event_at":"2026-10-18T12:00:00Z"}`, i)
			if w := request(handler, http.MethodPost, "/notes", body, token); w.Code != http.StatusOK {
				errs <- fmt.Errorf("request %d: status %d, body %s", i, w.Code, w.Body.String())
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}

	db.mu.Lock()
	gotIDs := make([]string, len(db.noteRequests))
	gotClientIDs := append([]int64(nil), db.noteClientIDs...)
	for i, req := range db.noteRequests {
		gotIDs[i] = req.RequestID
	}
	db.mu.Unlock()
	sort.Strings(gotIDs)
	if len(gotIDs) != requests {
		t.Fatalf("SubmitNote calls = %d, want %d", len(gotIDs), requests)
	}
	for i := 0; i < requests; i++ {
		want := fmt.Sprintf("request-%02d", i)
		if gotIDs[i] != want {
			t.Fatalf("request IDs = %v, missing or overwritten %q", gotIDs, want)
		}
		if gotClientIDs[i] != db.client.ID {
			t.Fatalf("client ID at call %d = %d, want %d", i, gotClientIDs[i], db.client.ID)
		}
	}
}
