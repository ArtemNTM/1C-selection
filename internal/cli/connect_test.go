package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/bochkarevart/1c-selection/internal/model"
)

func TestConnectDrainsPendingNotificationsAcrossBatches(t *testing.T) {
	const count = 1001
	pending := make(map[int64]bool)
	for id := int64(1); id <= count; id++ {
		pending[id] = true
	}
	var output bytes.Buffer
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/hello" {
			helloResponse(w, 7)
			return
		}
		if r.Header.Get("Authorization") != "Bearer session-token" {
			t.Error("missing session token")
		}
		if r.URL.Path == "/notifications" {
			values := make([]model.Notification, 0)
			for id := int64(1); id <= count && len(values) < 1000; id++ {
				if pending[id] {
					values = append(values, model.Notification{
						ID:      id,
						Message: fmt.Sprintf("notification-%d!", id),
					})
				}
			}
			_ = json.NewEncoder(w).Encode(values)
			return
		}
		parts := strings.Split(r.URL.Path, "/")
		id, err := strconv.ParseInt(parts[2], 10, 64)
		if err != nil || !strings.Contains(output.String(), fmt.Sprintf("notification-%d!", id)) {
			t.Error("notification acknowledged before successful output")
		}
		delete(pending, id)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	if err := runCLI(server.Client(), t.TempDir(), server.URL, &output, "connect"); err != nil {
		t.Fatal(err)
	}
	if len(pending) != 0 {
		t.Fatalf("%d notifications remain after reconnect", len(pending))
	}
	decoder := json.NewDecoder(&output)
	for _, want := range []int{1000, 1} {
		var batch struct {
			Notifications []model.Notification `json:"notifications"`
		}
		if err := decoder.Decode(&batch); err != nil || len(batch.Notifications) != want {
			t.Fatalf("batch size=%d want=%d error=%v", len(batch.Notifications), want, err)
		}
	}
}

func TestConnectKeepsNotificationsWhenOutputFails(t *testing.T) {
	acks := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/hello":
			helloResponse(w, 7)
		case "/notifications":
			_ = json.NewEncoder(w).Encode([]model.Notification{{
				ID:      1,
				Message: "pending",
			}})
		default:
			acks++
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	defer server.Close()
	if err := runCLI(server.Client(), t.TempDir(), server.URL, failingWriter{}, "connect"); err == nil {
		t.Fatal("output failure ignored")
	}
	if acks != 0 {
		t.Fatal("notification was lost after output failure")
	}
	if err := runCLI(server.Client(), t.TempDir(), server.URL, io.Discard, "connect", "--no-ack"); err != nil {
		t.Fatal(err)
	}
	if acks != 0 {
		t.Fatal("no-ack connection acknowledged notification")
	}
}
