package service_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"math"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/bochkarevart/1c-selection/internal/model"
	"github.com/bochkarevart/1c-selection/internal/service"
	"github.com/bochkarevart/1c-selection/internal/store"
	"github.com/bochkarevart/1c-selection/migrations"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var testDatabaseURL = flag.String("test-database-url", "", "PostgreSQL URL for integration tests")

type fixture struct {
	s   *service.Service
	db  *store.Store
	url string
	cfg service.Config
	now time.Time
}

func newFixture(t *testing.T, threshold float64, congratulate int) *fixture {
	t.Helper()
	databaseURL := *testDatabaseURL
	if databaseURL == "" {
		t.Skip("PostgreSQL integration test: pass -args -test-database-url URL")
	}
	u, err := url.Parse(databaseURL)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") {
		t.Fatal("test-database-url must be a postgres:// or postgresql:// URL")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	admin, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	var random [10]byte
	if _, err := rand.Read(random[:]); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	schema := "times_test_" + hex.EncodeToString(random[:])
	quotedSchema := pgx.Identifier{schema}.Sanitize()
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+quotedSchema); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	f := &fixture{
		url: u.String(),
		now: time.Date(2026, 10, 18, 10, 0, 0, 0, time.UTC),
	}
	f.cfg = service.Config{
		Threshold:    threshold,
		HalfLife:     time.Hour,
		SessionTTL:   24 * time.Hour,
		Congratulate: congratulate,
		Now: func() time.Time {
			return f.now
		},
	}
	t.Cleanup(func() {
		if f.db != nil {
			f.db.Close()
		}
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cleanupCancel()
		if _, err := admin.Exec(cleanupCtx, "DROP SCHEMA "+quotedSchema+" CASCADE"); err != nil {
			t.Errorf("remove test schema: %v", err)
		}
		admin.Close()
	})
	f.open(t)
	if err := migrations.Migrate(ctx, f.db); err != nil {
		t.Fatal(err)
	}

	if err := migrations.Migrate(ctx, f.db); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *fixture) open(t *testing.T) {
	t.Helper()
	if err := f.cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, f.url)
	if err != nil {
		t.Fatal(err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		t.Fatal(err)
	}
	f.db = store.NewStore(pool)
	f.s = service.New(f.db, nil, f.cfg)
}

func (f *fixture) client(t *testing.T, name string, trust float64) model.ClientCredentials {
	t.Helper()
	kind := "freelance"
	if trust == 1 {
		kind = "staff"
	}
	c, err := f.s.CreateClient(context.Background(), model.CreateClientRequest{
		Name:  name,
		Kind:  kind,
		Role:  "reporter",
		Trust: trust,
	})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func (f *fixture) note(id, topic string) model.NoteRequest {
	return model.NoteRequest{
		RequestID: id,
		Rubric:    "Город",
		Topic:     topic,
		Body:      "Подтверждаю",
		EventAt:   f.now,
	}
}

func submit(t *testing.T, f *fixture, clientID int64, note model.NoteRequest) model.Receipt {
	t.Helper()
	r, err := f.s.SubmitNote(context.Background(), clientID, note)
	if err != nil {
		t.Fatalf("submit %s: %v", note.RequestID, err)
	}
	return r
}

func story(t *testing.T, f *fixture, storyID int64) model.Story {
	t.Helper()
	s, err := f.s.Story(context.Background(), storyID)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func stats(t *testing.T, f *fixture, clientID int64) model.Stats {
	t.Helper()
	s, err := f.s.Stats(context.Background(), clientID)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func notifications(t *testing.T, f *fixture, clientID int64) []model.Notification {
	t.Helper()
	n, err := f.s.Notifications(context.Background(), clientID, 100)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func closeFloat(t *testing.T, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 1e-10 {
		t.Fatalf("got %.12f, want %.12f", got, want)
	}
}

func errorCode(t *testing.T, err error, code string) {
	t.Helper()
	var appErr *model.Error
	if !errors.As(err, &appErr) || appErr.Code != code {
		t.Fatalf("got error %v, want code %q", err, code)
	}
}

func TestPostgresRetryAndFirstVoice(t *testing.T) {
	f := newFixture(t, 2, 3)
	a := f.client(t, "tonny", .25)
	b := f.client(t, "pyu", .5)
	ctx := context.Background()

	got, err := f.s.Authenticate(ctx, a.Client.Name, a.Token)
	if err != nil || got.ID != a.Client.ID {
		t.Fatalf("authenticate: client=%+v err=%v", got, err)
	}
	_, err = f.s.Authenticate(ctx, a.Client.Name, "incorrect-key")
	errorCode(t, err, "unauthorized")

	note := f.note("same-request", "Обрушился мост")
	first := submit(t, f, a.Client.ID, note)
	if first.Status != "accepted" || !first.Contributes || first.Published {
		t.Fatalf("first receipt: %+v", first)
	}
	duplicate := submit(t, f, a.Client.ID, note)
	if duplicate.Status != "duplicate" || duplicate.NoteID != first.NoteID || duplicate.StoryID != first.StoryID {
		t.Fatalf("duplicate receipt: %+v", duplicate)
	}
	changed := note
	changed.Body = "Другой текст"
	_, err = f.s.SubmitNote(ctx, a.Client.ID, changed)
	errorCode(t, err, "conflict")
	changed = note
	changed.Topic = "Другой сюжет"
	_, err = f.s.SubmitNote(ctx, a.Client.ID, changed)
	errorCode(t, err, "conflict")

	second := submit(t, f, a.Client.ID, f.note("new-request", note.Topic))
	if second.Contributes || second.Status != "accepted" {
		t.Fatalf("second distinct note must be accepted without a second voice: %+v", second)
	}

	other := submit(t, f, b.Client.ID, note)
	if !other.Contributes || other.NoteID == first.NoteID {
		t.Fatalf("different author's same request ID: %+v", other)
	}
	s := story(t, f, first.StoryID)
	if len(s.Participants) != 2 || s.PublishedAt != nil {
		t.Fatalf("story: %+v", s)
	}
	closeFloat(t, s.CurrentTrust, .75)
	st := stats(t, f, a.Client.ID)
	if st.NotesAccepted != 2 || st.NotesPublished != 0 || st.StoriesTouched != 1 {
		t.Fatalf("retries/conflicts changed statistics: %+v", st)
	}
	all, err := f.s.Stories(ctx, 100)
	if err != nil || len(all) != 1 {
		t.Fatalf("conflicting retry created another story: %+v, %v", all, err)
	}
}

func TestPostgresDecayAndArrivalOrder(t *testing.T) {
	f := newFixture(t, .75, 3)
	a := f.client(t, "old-source", 1)
	b := f.client(t, "second", .2)
	c := f.client(t, "third", .4)
	old := f.note("old", "Мост")
	old.EventAt = f.now.Add(-2 * time.Hour)
	r := submit(t, f, a.Client.ID, old)
	closeFloat(t, story(t, f, r.StoryID).CurrentTrust, .25)
	if r.Published {
		t.Fatal("an old note received full fresh weight")
	}
	submit(t, f, b.Client.ID, f.note("second", old.Topic))

	followup := submit(t, f, a.Client.ID, f.note("followup", old.Topic))
	if followup.Contributes || followup.Published {
		t.Fatalf("follow-up refreshed old voice: %+v", followup)
	}
	closeFloat(t, story(t, f, r.StoryID).CurrentTrust, .45)
	last := submit(t, f, c.Client.ID, f.note("third", old.Topic))
	if !last.Published {
		t.Fatal("fresh independent source did not trigger publication")
	}
	s := story(t, f, r.StoryID)
	if s.TrustAtPublish == nil || len(s.Participants) != 3 {
		t.Fatalf("published story: %+v", s)
	}
	closeFloat(t, *s.TrustAtPublish, .85)
	for i, client := range []int64{a.Client.ID, b.Client.ID, c.Client.ID} {
		p := s.Participants[i]
		if p.ClientID != client || p.Rank == nil || *p.Rank != i+1 {
			t.Fatalf("arrival order/rank at %d: %+v", i, p)
		}
	}

	future := f.note("future", "Другой сюжет")
	future.EventAt = f.now.Add(2 * time.Minute)
	_, err := f.s.SubmitNote(context.Background(), a.Client.ID, future)
	errorCode(t, err, "invalid")
	if st := stats(t, f, a.Client.ID); st.NotesAccepted != 2 {
		t.Fatalf("rejected future note was saved: %+v", st)
	}
}

func TestPostgresPublicationStatisticsAndDurableDelivery(t *testing.T) {
	f := newFixture(t, 1.1, 1)
	a := f.client(t, "first", .4)
	b := f.client(t, "second", .4)
	c := f.client(t, "third", .4)
	late := f.client(t, "late", 1)
	ctx := context.Background()
	r := submit(t, f, a.Client.ID, f.note("a1", "Мост"))
	submit(t, f, a.Client.ID, f.note("a2", "Мост"))
	submit(t, f, b.Client.ID, f.note("b1", "Мост"))
	published := submit(t, f, c.Client.ID, f.note("c1", "Мост"))
	if !published.Published {
		t.Fatal("threshold was reached without publication")
	}
	before := story(t, f, r.StoryID)
	if before.PublishedAt == nil || before.TrustAtPublish == nil || len(before.Participants) != 3 {
		t.Fatalf("publication: %+v", before)
	}
	for i, p := range before.Participants {
		if p.Rank == nil || *p.Rank != i+1 {
			t.Fatalf("all participants must retain ranks, including those not congratulated: %+v", p)
		}
	}
	lateReceipt := submit(t, f, late.Client.ID, f.note("late", "Мост"))
	if !lateReceipt.Published || lateReceipt.Contributes {
		t.Fatalf("late note received a voice: %+v", lateReceipt)
	}
	submit(t, f, a.Client.ID, f.note("a3", "Мост"))

	st := stats(t, f, a.Client.ID)
	if st.NotesAccepted != 3 || st.NotesPublished != 1 || st.StoriesTouched != 1 || st.Sensations != 1 || st.AverageRank != 1 {
		t.Fatalf("first author's statistics: %+v", st)
	}
	closeFloat(t, st.PublicationShare, 1.0/3)
	if len(st.Stories) != 1 || st.Stories[0].Rank == nil || *st.Stories[0].Rank != 1 {
		t.Fatalf("first author's stories: %+v", st.Stories)
	}
	thirdStats := stats(t, f, c.Client.ID)
	if thirdStats.NotesPublished != 1 || thirdStats.AverageRank != 3 || thirdStats.Sensations != 0 {
		t.Fatalf("non-congratulated participant lost publication credit: %+v", thirdStats)
	}
	lateStats := stats(t, f, late.Client.ID)
	if lateStats.NotesAccepted != 1 || lateStats.NotesPublished != 0 || lateStats.AverageRank != 0 || lateStats.StoriesTouched != 1 || len(lateStats.Stories) != 1 || lateStats.Stories[0].Rank != nil {
		t.Fatalf("late author received publication credit: %+v", lateStats)
	}
	all, err := f.s.AllStats(ctx)
	if err != nil || len(all) != 4 {
		t.Fatalf("all statistics: %+v, %v", all, err)
	}
	if len(notifications(t, f, b.Client.ID)) != 0 || len(notifications(t, f, c.Client.ID)) != 0 || len(notifications(t, f, late.Client.ID)) != 0 {
		t.Fatal("notifications exceeded congratulations limit or included a late author")
	}
	pending := notifications(t, f, a.Client.ID)
	if len(pending) != 1 || pending[0].Rank != 1 || pending[0].StoryID != r.StoryID {
		t.Fatalf("winner notification: %+v", pending)
	}

	f.db.Close()
	f.s = nil
	f.open(t)
	again := notifications(t, f, a.Client.ID)
	if len(again) != 1 || again[0].ID != pending[0].ID {
		t.Fatalf("notification did not survive reopen: %+v", again)
	}
	if err := f.s.AckNotification(ctx, b.Client.ID, pending[0].ID); err == nil {
		t.Fatal("another author acknowledged a notification")
	}
	if len(notifications(t, f, a.Client.ID)) != 1 {
		t.Fatal("another author's ack removed the notification")
	}
	if err := f.s.AckNotification(ctx, a.Client.ID, pending[0].ID); err != nil {
		t.Fatal(err)
	}
	if err := f.s.AckNotification(ctx, a.Client.ID, pending[0].ID); err != nil {
		t.Fatalf("ack is not idempotent: %v", err)
	}
	if len(notifications(t, f, a.Client.ID)) != 0 {
		t.Fatal("acknowledged notification is still pending")
	}

	zero := 0.0
	if _, err := f.s.UpdateClient(ctx, a.Client.ID, model.UpdateClientRequest{Trust: &zero}); err != nil {
		t.Fatal(err)
	}
	f.now = f.now.Add(24 * time.Hour)
	after := story(t, f, r.StoryID)
	if after.PublishedAt == nil || !after.PublishedAt.Equal(*before.PublishedAt) || after.TrustAtPublish == nil || *after.TrustAtPublish != *before.TrustAtPublish || len(after.Participants) != 3 {
		t.Fatalf("historical publication changed: before=%+v after=%+v", before, after)
	}
}

func TestPostgresUsesCurrentTrustOnNextAcceptedNote(t *testing.T) {
	f := newFixture(t, .6, 1)
	a := f.client(t, "tonny", .25)
	r := submit(t, f, a.Client.ID, f.note("first", "Мост"))
	updatedTrust := .75
	if _, err := f.s.UpdateClient(context.Background(), a.Client.ID, model.UpdateClientRequest{Trust: &updatedTrust}); err != nil {
		t.Fatal(err)
	}
	if s := story(t, f, r.StoryID); s.PublishedAt != nil {
		t.Fatal("changing trust published a story without a new note")
	}

	second := submit(t, f, a.Client.ID, f.note("second", "Мост"))
	if second.Contributes || !second.Published {
		t.Fatalf("current trust was not applied on the next accepted note: %+v", second)
	}
	s := story(t, f, r.StoryID)
	if s.TrustAtPublish == nil {
		t.Fatal("missing publication trust")
	}
	closeFloat(t, *s.TrustAtPublish, .75)
}

func TestPostgresConcurrentPublication(t *testing.T) {
	f := newFixture(t, .9, 3)
	const n = 10
	clients := make([]model.ClientCredentials, n)
	for i := range clients {
		clients[i] = f.client(t, fmt.Sprintf("client-%d", i), .2)
	}
	type result struct {
		r   model.Receipt
		err error
	}
	results := make(chan result, n)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range clients {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			r, err := f.s.SubmitNote(context.Background(), clients[i].Client.ID, f.note(fmt.Sprintf("request-%d", i), "Мост"))
			results <- result{r, err}
		}(i)
	}
	close(start)
	wg.Wait()
	close(results)
	var storyID int64
	voices := 0
	for result := range results {
		if result.err != nil {
			t.Errorf("concurrent submission: %v", result.err)
			continue
		}
		if storyID != 0 && result.r.StoryID != storyID {
			t.Errorf("two stories for one rubric/topic: %d and %d", storyID, result.r.StoryID)
		}
		storyID = result.r.StoryID
		if result.r.Contributes {
			voices++
		}
	}
	if t.Failed() {
		t.FailNow()
	}
	s := story(t, f, storyID)
	if voices != 5 || s.PublishedAt == nil || len(s.Participants) != 5 || s.TrustAtPublish == nil {
		t.Fatalf("publication should freeze the first five independent voices: voices=%d story=%+v", voices, s)
	}
	closeFloat(t, *s.TrustAtPublish, 1)
	for i, p := range s.Participants {
		if p.Rank == nil || *p.Rank != i+1 {
			t.Fatalf("concurrent publication ranks are not complete: %+v", s.Participants)
		}
	}
	notificationCount := 0
	for _, c := range clients {
		notificationCount += len(notifications(t, f, c.Client.ID))
		if st := stats(t, f, c.Client.ID); st.NotesAccepted != 1 {
			t.Fatalf("concurrent note was lost or duplicated: %+v", st)
		}
	}
	if notificationCount != 3 {
		t.Fatalf("publication produced %d notifications, want 3", notificationCount)
	}
}

func TestPostgresConcurrentRetries(t *testing.T) {
	f := newFixture(t, 2, 3)
	a := f.client(t, "tonny", .25)
	note := f.note("retry-request", "Мост")
	const n = 8
	type result struct {
		r   model.Receipt
		err error
	}
	results := make(chan result, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, err := f.s.SubmitNote(context.Background(), a.Client.ID, note)
			results <- result{r, err}
		}()
	}
	wg.Wait()
	close(results)
	accepted, duplicates := 0, 0
	var noteID int64
	for result := range results {
		if result.err != nil {
			t.Errorf("concurrent retry: %v", result.err)
			continue
		}
		if noteID != 0 && result.r.NoteID != noteID {
			t.Errorf("retry returned another note: %+v", result.r)
		}
		noteID = result.r.NoteID
		switch result.r.Status {
		case "accepted":
			accepted++
		case "duplicate":
			duplicates++
		default:
			t.Errorf("unexpected receipt: %+v", result.r)
		}
	}
	if accepted != 1 || duplicates != n-1 {
		t.Fatalf("accepted=%d duplicates=%d, want 1 and %d", accepted, duplicates, n-1)
	}
	if st := stats(t, f, a.Client.ID); st.NotesAccepted != 1 || st.StoriesTouched != 1 {
		t.Fatalf("concurrent retry was counted twice: %+v", st)
	}
}
