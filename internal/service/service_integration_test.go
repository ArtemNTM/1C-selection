package service_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/url"
	"reflect"
	"testing"
	"time"

	"github.com/bochkarevart/1c-selection/internal/cache"
	"github.com/bochkarevart/1c-selection/internal/model"
	"github.com/bochkarevart/1c-selection/internal/service"
	"github.com/bochkarevart/1c-selection/internal/store"
	"github.com/bochkarevart/1c-selection/migrations"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPostgresWritesInvalidateCachedViewsAndAuthStaysLive(t *testing.T) {
	databaseURL := *testDatabaseURL
	if databaseURL == "" {
		t.Skip("PostgreSQL integration test: pass -args -test-database-url URL")
	}
	u, err := url.Parse(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	admin, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	var random [10]byte
	if _, err = rand.Read(random[:]); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	schema := "times_service_test_" + hex.EncodeToString(random[:])
	quoted := pgx.Identifier{schema}.Sanitize()
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+quoted); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	query := u.Query()
	query.Set("search_path", schema)
	u.RawQuery = query.Encode()
	now := time.Date(2026, 10, 18, 10, 0, 0, 0, time.UTC)
	cfg := service.Config{
		Threshold:    .7,
		HalfLife:     time.Hour,
		SessionTTL:   24 * time.Hour,
		Congratulate: 0,
		Now: func() time.Time {
			return now
		},
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.New(ctx, u.String())
	if err != nil {
		t.Fatal(err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		t.Fatal(err)
	}
	db := store.NewStore(pool)
	t.Cleanup(func() {
		db.Close()
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cleanupCancel()
		if _, err := admin.Exec(cleanupCtx, "DROP SCHEMA "+quoted+" CASCADE"); err != nil {
			t.Errorf("remove test schema: %v", err)
		}
		admin.Close()
	})
	if err := migrations.Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	liveDB, err := pgx.Connect(ctx, u.String())
	if err != nil {
		t.Fatal(err)
	}
	defer liveDB.Close(ctx)
	c := cache.NewWithClock(func(ctx context.Context) (cache.Snapshot, error) {
		return service.LoadCache(ctx, db, cfg)
	}, time.Hour, cfg.Now)
	if err := c.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	svc := service.New(db, c, cfg)

	first, err := svc.CreateClient(ctx, model.CreateClientRequest{
		Name:  "first",
		Kind:  "staff",
		Role:  "reporter",
		Trust: .4,
	})
	if err != nil {
		t.Fatal(err)
	}

	clients, err := svc.ListClients(ctx)
	if err != nil || len(clients) != 1 || clients[0].ID != first.Client.ID {
		t.Fatalf("clients after create: %#v, %v", clients, err)
	}
	if err = c.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	request := model.NoteRequest{
		RequestID: "first",
		Rubric:    "city",
		Topic:     "bridge",
		EventAt:   now,
	}
	firstNote, err := svc.SubmitNote(ctx, first.Client.ID, request)
	if err != nil || firstNote.Status != "accepted" {
		t.Fatalf("first note: %#v, %v", firstNote, err)
	}
	uncachedStories, err := svc.Stories(ctx, 10)
	if err != nil || len(uncachedStories) != 1 {
		t.Fatalf("uncached stories: %#v, %v", uncachedStories, err)
	}
	if err = c.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	cachedStories, err := svc.Stories(ctx, 10)
	if err != nil || !reflect.DeepEqual(cachedStories, uncachedStories) {
		t.Fatalf("cached stories differ: cached=%#v uncached=%#v err=%v", cachedStories, uncachedStories, err)
	}
	cachedStory, err := svc.Story(ctx, firstNote.StoryID)
	if err != nil || len(cachedStory.Participants) != 1 {
		t.Fatalf("cached detail lost participants: %#v, %v", cachedStory, err)
	}
	second, err := svc.CreateClient(ctx, model.CreateClientRequest{
		Name:  "second",
		Kind:  "staff",
		Role:  "reporter",
		Trust: .4,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = c.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	request.RequestID = "second"
	published, err := svc.SubmitNote(ctx, second.Client.ID, request)
	if err != nil || !published.Published {
		t.Fatalf("publishing note: %#v, %v", published, err)
	}

	story, err := svc.Story(ctx, firstNote.StoryID)
	if err != nil || story.PublishedAt == nil || len(story.Participants) != 2 {
		t.Fatalf("story after publish: %#v, %v", story, err)
	}
	if _, err = liveDB.Exec(ctx, "UPDATE clients SET role='editor' WHERE id=$1", first.Client.ID); err != nil {
		t.Fatal(err)
	}
	live, err := svc.ClientByID(ctx, first.Client.ID)
	if err != nil || live.Role != "editor" {
		t.Fatalf("live authorization client: %#v, %v", live, err)
	}
}

func TestPostgresCacheBypassesFutureContributionsUntilRefresh(t *testing.T) {
	f := newFixture(t, 2, 0)
	f.cfg.HalfLife = time.Minute
	f.s = service.New(f.db, nil, f.cfg)
	client := f.client(t, "future-source", .5)
	future := f.note("future", "Future event")
	future.EventAt = f.now.Add(time.Minute)
	futureReceipt := submit(t, f, client.Client.ID, future)
	currentReceipt := submit(t, f, client.Client.ID, f.note("current", "Current event"))

	ctx := context.Background()
	c := cache.NewWithClock(func(ctx context.Context) (cache.Snapshot, error) {
		return service.LoadCache(ctx, f.db, f.cfg)
	}, f.cfg.HalfLife, f.cfg.Now)
	svc := service.New(f.db, c, f.cfg)
	if err := c.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	if _, hit := c.Story(futureReceipt.StoryID); hit {
		t.Fatal("future contribution was cached before its decay started")
	}
	if _, hit := c.Story(currentReceipt.StoryID); !hit {
		t.Fatal("unaffected story was not cached")
	}
	if _, hit := c.Stories(10); hit {
		t.Fatal("partial snapshot was served as a complete list")
	}

	for _, elapsed := range []time.Duration{30 * time.Second, 60 * time.Second} {
		f.now = f.now.Add(elapsed)
		cachedView, err := svc.Story(ctx, futureReceipt.StoryID)
		if err != nil {
			t.Fatal(err)
		}
		liveView := story(t, f, futureReceipt.StoryID)
		closeFloat(t, cachedView.CurrentTrust, liveView.CurrentTrust)
		if _, hit := c.Story(futureReceipt.StoryID); hit {
			t.Fatal("future contribution became cached without a fresh snapshot")
		}
		listed, err := svc.Stories(ctx, 10)
		if err != nil || len(listed) != 2 {
			t.Fatalf("list with an uncacheable story: %#v, %v", listed, err)
		}
	}

	if err := c.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	if _, hit := c.Story(futureReceipt.StoryID); !hit {
		t.Fatal("past contribution did not become cacheable after refresh")
	}
	if _, hit := c.Stories(10); !hit {
		t.Fatal("complete snapshot did not become cacheable after refresh")
	}
	f.now = f.now.Add(30 * time.Second)
	cachedView, err := svc.Story(ctx, futureReceipt.StoryID)
	if err != nil {
		t.Fatal(err)
	}
	closeFloat(t, cachedView.CurrentTrust, .25)
	closeFloat(t, cachedView.CurrentTrust, story(t, f, futureReceipt.StoryID).CurrentTrust)
}
