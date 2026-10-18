package service_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/bochkarevart/1c-selection/internal/model"
	"github.com/jackc/pgx/v5"
)

func TestPostgresPublicationRollback(t *testing.T) {
	f := newFixture(t, .6, 2)
	firstClient := f.client(t, "first-source", .4)
	secondClient := f.client(t, "second-source", .4)
	first := submit(t, f, firstClient.Client.ID, f.note("first-request", "Пожар в порту"))
	if first.Published || !first.Contributes {
		t.Fatalf("first receipt: %+v", first)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, err := pgx.Connect(ctx, f.url)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(context.Background())
	if _, err = conn.Exec(ctx, `CREATE FUNCTION fail_notification_insert() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'injected notification failure';
END;
$$`); err != nil {
		t.Fatal(err)
	}
	if _, err = conn.Exec(ctx, `CREATE TRIGGER fail_notification_insert
BEFORE INSERT ON notifications FOR EACH ROW EXECUTE FUNCTION fail_notification_insert()`); err != nil {
		t.Fatal(err)
	}

	secondNote := f.note("second-request", "Пожар в порту")
	_, err = f.s.SubmitNote(context.Background(), secondClient.Client.ID, secondNote)
	if err == nil {
		t.Fatal("submission whose notification insert fails succeeded")
	}

	beforeRetry := story(t, f, first.StoryID)
	if beforeRetry.PublishedAt != nil || len(beforeRetry.Participants) != 1 || beforeRetry.Participants[0].Rank != nil {
		t.Fatalf("failed publication leaked story changes: %+v", beforeRetry)
	}
	if st := stats(t, f, secondClient.Client.ID); st.NotesAccepted != 0 || st.StoriesTouched != 0 {
		t.Fatalf("failed note was persisted: %+v", st)
	}

	if _, err = conn.Exec(ctx, "DROP TRIGGER fail_notification_insert ON notifications"); err != nil {
		t.Fatal(err)
	}
	if _, err = conn.Exec(ctx, "DROP FUNCTION fail_notification_insert()"); err != nil {
		t.Fatal(err)
	}
	retry := submit(t, f, secondClient.Client.ID, secondNote)
	if retry.Status != "accepted" || !retry.Contributes || !retry.Published || retry.StoryID != first.StoryID {
		t.Fatalf("retry receipt: %+v", retry)
	}
	afterRetry := story(t, f, first.StoryID)
	if afterRetry.PublishedAt == nil || len(afterRetry.Participants) != 2 {
		t.Fatalf("retried publication: %+v", afterRetry)
	}
	for i, participant := range afterRetry.Participants {
		if participant.Rank == nil || *participant.Rank != i+1 {
			t.Fatalf("publication ranks: %+v", afterRetry.Participants)
		}
	}
	if got := notifications(t, f, firstClient.Client.ID); len(got) != 1 || got[0].StoryID != first.StoryID || got[0].Rank != 1 {
		t.Fatalf("first notification: %+v", got)
	}
	if got := notifications(t, f, secondClient.Client.ID); len(got) != 1 || got[0].StoryID != first.StoryID || got[0].Rank != 2 {
		t.Fatalf("second notification: %+v", got)
	}
}

func TestPostgresConcurrentConflictingRequest(t *testing.T) {
	f := newFixture(t, 2, 0)
	client := f.client(t, "same-request-author", .4)
	requests := []model.NoteRequest{
		f.note("same-request", "Первый вариант сюжета"),
		f.note("same-request", "Второй вариант сюжета"),
	}
	type result struct {
		index   int
		receipt model.Receipt
		err     error
	}
	results := make(chan result, len(requests))
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i, request := range requests {
		wg.Add(1)
		go func(i int, request model.NoteRequest) {
			defer wg.Done()
			<-start
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			receipt, err := f.s.SubmitNote(ctx, client.Client.ID, request)
			results <- result{
				index:   i,
				receipt: receipt,
				err:     err,
			}
		}(i, request)
	}
	close(start)
	wg.Wait()
	close(results)

	accepted := result{index: -1}
	conflicts := 0
	for got := range results {
		if got.err == nil {
			if accepted.index != -1 {
				t.Errorf("two accepted receipts: %+v and %+v", accepted.receipt, got.receipt)
			}
			accepted = got
			continue
		}
		var appErr *model.Error
		if errors.As(got.err, &appErr) && appErr.Code == "conflict" {
			conflicts++
			continue
		}
		t.Errorf("concurrent submit %d: %v", got.index, got.err)
	}
	if accepted.index == -1 || conflicts != 1 || t.Failed() {
		t.Fatalf("accepted=%+v conflicts=%d", accepted, conflicts)
	}
	if accepted.receipt.Status != "accepted" || accepted.receipt.NoteID == 0 || accepted.receipt.StoryID == 0 {
		t.Fatalf("accepted receipt identity: %+v", accepted.receipt)
	}
	stored := story(t, f, accepted.receipt.StoryID)
	if stored.Rubric != requests[accepted.index].Rubric || stored.Topic != requests[accepted.index].Topic || len(stored.Participants) != 1 {
		t.Fatalf("accepted receipt points to wrong story: receipt=%+v story=%+v", accepted.receipt, stored)
	}
	all, err := f.s.Stories(context.Background(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 || all[0].ID != accepted.receipt.StoryID {
		t.Fatalf("conflicting request left an orphan story: %+v", all)
	}
	conn, err := pgx.Connect(context.Background(), f.url)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(context.Background())
	var noteCount int
	if err := conn.QueryRow(context.Background(), "SELECT count(*) FROM notes").Scan(&noteCount); err != nil {
		t.Fatal(err)
	}
	if noteCount != 1 {
		t.Fatalf("stored %d notes, want 1", noteCount)
	}
	if st := stats(t, f, client.Client.ID); st.NotesAccepted != 1 || st.StoriesTouched != 1 {
		t.Fatalf("concurrent conflict stored more than one note: %+v", st)
	}
}
