package cache

import (
	"context"
	"errors"
	"math"
	"sync"
	"testing"
	"time"

	"github.com/bochkarevart/1c-selection/internal/model"
)

type sourceFunc = Source

func sample(asOf time.Time) Snapshot {
	rank := 1
	return Snapshot{
		AsOf: asOf,
		Clients: []model.Client{{
			ID:   2,
			Name: "two",
		}, {
			ID:   1,
			Name: "one",
		}},
		Stories: []model.Story{
			{
				ID:           2,
				Topic:        "second",
				CurrentTrust: 8,
				Participants: []model.Participant{{
					ClientID: 1,
					Rank:     &rank,
				}},
			},
			{
				ID:           5,
				Topic:        "fifth",
				CurrentTrust: 4,
			},
		},
	}
}

func TestRefreshFailureRetainsLastSnapshot(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	var calls int
	c := NewWithClock(sourceFunc(func(context.Context) (Snapshot, error) {
		calls++
		if calls == 2 {
			return Snapshot{}, errors.New("database unavailable")
		}
		return sample(now), nil
	}), time.Hour, func() time.Time {
		return now
	})
	if err := c.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := c.Refresh(context.Background()); err == nil {
		t.Fatal("Refresh error = nil")
	}
	clients, ok := c.Clients()
	if !ok || len(clients) != 2 || clients[0].ID != 1 {
		t.Fatalf("last good snapshot lost: %#v, %v", clients, ok)
	}
}

func TestInvalidationWinsOverInflightRefresh(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	var calls int
	var mu sync.Mutex
	c := New(sourceFunc(func(context.Context) (Snapshot, error) {
		mu.Lock()
		calls++
		call := calls
		mu.Unlock()
		if call == 2 {
			close(started)
			<-release
		}
		return sample(time.Now()), nil
	}), time.Hour)
	if err := c.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		done <- c.Refresh(context.Background())
	}()
	<-started
	c.InvalidateStory(5)
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if _, ok := c.Story(5); ok {
		t.Fatal("stale in-flight snapshot restored invalidated story")
	}
	if _, ok := c.Stories(10); ok {
		t.Fatal("invalidated complete list reported a cache hit")
	}
	if clients, ok := c.Clients(); !ok || len(clients) != 2 {
		t.Fatalf("story invalidation changed client cache: %#v, %v", clients, ok)
	}
}

func TestGettersCopySortLimitAndDecay(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	now := base
	c := NewWithClock(sourceFunc(func(context.Context) (Snapshot, error) {
		return sample(base), nil
	}), time.Hour, func() time.Time {
		return now
	})
	if err := c.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	clients, ok := c.Clients()
	if !ok || clients[0].ID != 1 || clients[1].ID != 2 {
		t.Fatalf("clients = %#v", clients)
	}
	stories, ok := c.Stories(1)
	if !ok || len(stories) != 1 || stories[0].ID != 5 {
		t.Fatalf("stories = %#v", stories)
	}
	allStories, ok := c.Stories(10)
	if !ok || len(allStories) != 2 {
		t.Fatalf("all stories = %#v, hit = %v", allStories, ok)
	}
	for _, listed := range allStories {
		if listed.Participants != nil {
			t.Fatalf("list includes participants for story %d: %#v", listed.ID, listed.Participants)
		}
	}
	story, ok := c.Story(2)
	if !ok {
		t.Fatal("missing cached story")
	}
	*story.Participants[0].Rank = 99
	story.Participants = append(story.Participants, model.Participant{})
	again, _ := c.Story(2)
	if len(again.Participants) != 1 || *again.Participants[0].Rank != 1 {
		t.Fatalf("cache data was mutated: %#v", again.Participants)
	}
	now = base.Add(time.Hour)
	decayed, _ := c.Story(2)
	if math.Abs(decayed.CurrentTrust-4) > 1e-9 {
		t.Fatalf("trust = %v, want 4", decayed.CurrentTrust)
	}
	published := 8.0
	c2 := NewWithClock(sourceFunc(func(context.Context) (Snapshot, error) {
		return Snapshot{
			AsOf: base,
			Stories: []model.Story{{
				ID:             7,
				CurrentTrust:   8,
				TrustAtPublish: &published,
			}},
		}, nil
	}), time.Hour, func() time.Time {
		return now
	})
	if err := c2.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	fixed, _ := c2.Story(7)
	if fixed.CurrentTrust != 8 {
		t.Fatalf("published trust decayed to %v", fixed.CurrentTrust)
	}
}
