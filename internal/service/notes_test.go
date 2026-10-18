package service

import (
	"math"
	"testing"
	"time"

	"github.com/bochkarevart/1c-selection/internal/model"
)

func TestDecayUsesEventAge(t *testing.T) {
	now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	for _, tt := range []struct {
		name string
		age  time.Duration
		want float64
	}{
		{"fresh", 0, 0.8}, {"one half-life", 6 * time.Hour, 0.4}, {"two half-lives", 12 * time.Hour, 0.2}, {"clock skew", -time.Second, 0.8},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got := decayedTrust(0.8, now.Add(-tt.age), now, 6*time.Hour)
			if math.Abs(got-tt.want) > 1e-12 {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestEquivalentTimezonesHaveSameIdempotencyHash(t *testing.T) {
	req := model.NoteRequest{
		RequestID: "one",
		Rubric:    "Город",
		Topic:     "Мост",
		EventAt:   time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC),
	}
	a, err := noteHash(req)
	if err != nil {
		t.Fatal(err)
	}
	req.EventAt = req.EventAt.In(time.FixedZone("MSK", 3*60*60))
	b, err := noteHash(req)
	if err != nil {
		t.Fatal(err)
	}
	if a != b {
		t.Fatal("same instant must have the same hash regardless of timezone")
	}
	req.Body = "changed"
	c, err := noteHash(req)
	if err != nil {
		t.Fatal(err)
	}
	if a == c {
		t.Fatal("changed payload must have a different hash")
	}
}

func TestValidateNoteAcceptsOldEventsAndRejectsFuture(t *testing.T) {
	now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	req := model.NoteRequest{
		RequestID: "one",
		Rubric:    "Город",
		Topic:     "Мост",
		EventAt:   now.AddDate(-1, 0, 0),
	}
	if err := validateNote(req, now); err != nil {
		t.Fatalf("old event must be accepted: %v", err)
	}
	req.EventAt = now.Add(2 * time.Minute)
	if err := validateNote(req, now); err == nil {
		t.Fatal("future event must be rejected")
	}
}
