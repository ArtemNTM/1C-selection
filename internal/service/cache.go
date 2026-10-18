package service

import (
	"context"
	"fmt"

	"github.com/bochkarevart/1c-selection/internal/cache"
	"github.com/bochkarevart/1c-selection/internal/model"
	"github.com/bochkarevart/1c-selection/internal/store"
	"github.com/jackc/pgx/v5"
)

func LoadCache(ctx context.Context, db store.Database, cfg Config) (cache.Snapshot, error) {
	tx, err := db.BeginTx(ctx, pgx.TxOptions{
		IsoLevel:   pgx.RepeatableRead,
		AccessMode: pgx.ReadOnly,
	})
	if err != nil {
		return cache.Snapshot{}, err
	}
	defer tx.Rollback(ctx)

	result := cache.Snapshot{
		AsOf:    cfg.Now().UTC(),
		Clients: make([]model.Client, 0),
		Stories: make([]model.Story, 0),
	}
	query, args, err := psql.Select(clientColumns...).
		From("clients").
		OrderBy("id").
		ToSql()
	if err != nil {
		return cache.Snapshot{}, err
	}
	rows, err := tx.Query(ctx, query, args...)
	if err != nil {
		return cache.Snapshot{}, err
	}
	for rows.Next() {
		client, err := scanClient(rows)
		if err != nil {
			rows.Close()
			return cache.Snapshot{}, err
		}
		result.Clients = append(result.Clients, client)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return cache.Snapshot{}, err
	}
	rows.Close()

	query, args, err = psql.Select(storyColumns...).
		From("stories").
		OrderBy("id").
		ToSql()
	if err != nil {
		return cache.Snapshot{}, err
	}
	rows, err = tx.Query(ctx, query, args...)
	if err != nil {
		return cache.Snapshot{}, err
	}
	storyIndex := make(map[int64]int)
	for rows.Next() {
		story, err := scanStory(rows)
		if err != nil {
			rows.Close()
			return cache.Snapshot{}, err
		}
		story.Participants = make([]model.Participant, 0)
		storyIndex[story.ID] = len(result.Stories)
		result.Stories = append(result.Stories, story)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return cache.Snapshot{}, err
	}
	rows.Close()

	query, args, err = psql.Select(
		"n.story_id", "n.client_id", "c.name", "n.id",
		"n.received_at", "n.rank", "c.trust", "n.event_at",
	).
		From("notes n").
		Join("clients c ON c.id = n.client_id").
		Where("n.is_contribution").
		OrderBy("n.story_id", "n.id").
		ToSql()
	if err != nil {
		return cache.Snapshot{}, err
	}
	rows, err = tx.Query(ctx, query, args...)
	if err != nil {
		return cache.Snapshot{}, err
	}
	for rows.Next() {
		var storyID int64
		var participant model.Participant
		var trust float64
		var eventAt = result.AsOf
		if err := rows.Scan(
			&storyID,
			&participant.ClientID,
			&participant.Name,
			&participant.FirstNoteID,
			&participant.ReceivedAt,
			&participant.Rank,
			&trust,
			&eventAt,
		); err != nil {
			rows.Close()
			return cache.Snapshot{}, err
		}
		index, ok := storyIndex[storyID]
		if !ok {
			rows.Close()
			return cache.Snapshot{}, fmt.Errorf("cache contribution %d references absent story %d", participant.FirstNoteID, storyID)
		}
		story := &result.Stories[index]
		story.Participants = append(story.Participants, participant)
		if story.TrustAtPublish == nil {
			story.CurrentTrust += decayedTrust(trust, eventAt, result.AsOf, cfg.HalfLife)
			// Future contributions have not started decaying, so their total
			// cannot yet be decayed uniformly from the snapshot timestamp.
			if eventAt.After(result.AsOf) {
				result.UncacheableStoryIDs = append(result.UncacheableStoryIDs, storyID)
			}
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return cache.Snapshot{}, err
	}
	rows.Close()
	for i := range result.Stories {
		if result.Stories[i].TrustAtPublish != nil {
			result.Stories[i].CurrentTrust = *result.Stories[i].TrustAtPublish
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return cache.Snapshot{}, err
	}
	return result, nil
}
