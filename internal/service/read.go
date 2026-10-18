package service

import (
	"context"
	"errors"
	"time"

	sq "github.com/Masterminds/squirrel"
	"github.com/bochkarevart/1c-selection/internal/model"
	"github.com/jackc/pgx/v5"
)

func (s *Service) Notifications(ctx context.Context, clientID int64, limit int) ([]model.Notification, error) {
	if limit < 1 || limit > 1000 {
		return nil, model.NewError("invalid", "limit must be between 1 and 1000")
	}
	query, args, err := psql.Select(
		"id", "client_id", "story_id", "rank", "message", "created_at", "acknowledged_at",
	).
		From("notifications").
		Where(sq.Eq{
			"client_id":       clientID,
			"acknowledged_at": nil,
		}).
		OrderBy("id").
		Limit(uint64(limit)).
		ToSql()
	if err != nil {
		return nil, err
	}
	rows, err := s.db.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]model.Notification, 0)
	for rows.Next() {
		var n model.Notification
		if err = rows.Scan(&n.ID, &n.ClientID, &n.StoryID, &n.Rank, &n.Message, &n.CreatedAt, &n.AcknowledgedAt); err != nil {
			return nil, err
		}
		result = append(result, n)
	}
	return result, rows.Err()
}

func (s *Service) AckNotification(ctx context.Context, clientID, notificationID int64) error {
	query, args, err := psql.Update("notifications").
		Set("acknowledged_at", sq.Expr("COALESCE(acknowledged_at, ?)", s.cfg.Now().UTC())).
		Where(sq.Eq{
			"id":        notificationID,
			"client_id": clientID,
		}).
		ToSql()
	if err != nil {
		return err
	}
	result, err := s.db.Exec(ctx, query, args...)
	if err != nil {
		return err
	}
	if result.RowsAffected() == 0 {
		return model.NewError("not_found", "notification not found")
	}
	return nil
}

func (s *Service) Stats(ctx context.Context, clientID int64) (model.Stats, error) {
	tx, err := s.db.BeginTx(ctx, pgx.TxOptions{
		IsoLevel:   pgx.RepeatableRead,
		AccessMode: pgx.ReadOnly,
	})
	if err != nil {
		return model.Stats{}, err
	}
	defer tx.Rollback(ctx)
	query, args, err := psql.Select(clientColumns...).
		From("clients").
		Where(sq.Eq{"id": clientID}).
		ToSql()
	if err != nil {
		return model.Stats{}, err
	}
	client, err := scanClient(tx.QueryRow(ctx, query, args...))
	if errors.Is(err, pgx.ErrNoRows) {
		return model.Stats{}, model.NewError("not_found", "client not found")
	}
	if err != nil {
		return model.Stats{}, err
	}
	result, err := statsFor(ctx, tx, client)
	if err != nil {
		return result, err
	}
	return result, tx.Commit(ctx)
}

func (s *Service) AllStats(ctx context.Context) ([]model.Stats, error) {
	tx, err := s.db.BeginTx(ctx, pgx.TxOptions{
		IsoLevel:   pgx.RepeatableRead,
		AccessMode: pgx.ReadOnly,
	})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	query, args, err := psql.Select(clientColumns...).
		From("clients").
		OrderBy("id").
		ToSql()
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	clients := make([]model.Client, 0)
	for rows.Next() {
		c, err := scanClient(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		clients = append(clients, c)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	result := make([]model.Stats, 0, len(clients))
	for _, client := range clients {
		stats, err := statsFor(ctx, tx, client)
		if err != nil {
			return nil, err
		}
		result = append(result, stats)
	}
	return result, tx.Commit(ctx)
}

func statsFor(ctx context.Context, tx pgx.Tx, client model.Client) (model.Stats, error) {
	result := model.Stats{
		Client:  client,
		Stories: make([]model.StoryParticipation, 0),
	}
	query, args, err := psql.Select(
		"count(*)",
		"count(rank)",
		"count(DISTINCT story_id)",
		"count(*) FILTER (WHERE rank = 1)",
		"COALESCE(avg(rank), 0)::double precision",
	).
		From("notes").
		Where(sq.Eq{"client_id": client.ID}).
		ToSql()
	if err != nil {
		return result, err
	}
	err = tx.QueryRow(ctx, query, args...).Scan(
		&result.NotesAccepted,
		&result.NotesPublished,
		&result.StoriesTouched,
		&result.Sensations,
		&result.AverageRank,
	)
	if err != nil {
		return result, err
	}
	if result.NotesAccepted > 0 {
		result.PublicationShare = float64(result.NotesPublished) / float64(result.NotesAccepted)
	}
	query, args, err = psql.Select("s.id", "s.rubric", "s.topic", "s.published_at", "min(n.rank)").
		From("notes n").
		Join("stories s ON s.id = n.story_id").
		Where(sq.Eq{"n.client_id": client.ID}).
		GroupBy("s.id", "s.rubric", "s.topic", "s.published_at").
		OrderBy("s.id").
		ToSql()
	if err != nil {
		return result, err
	}
	rows, err := tx.Query(ctx, query, args...)
	if err != nil {
		return result, err
	}
	defer rows.Close()
	for rows.Next() {
		var story model.StoryParticipation
		if err = rows.Scan(&story.StoryID, &story.Rubric, &story.Topic, &story.PublishedAt, &story.Rank); err != nil {
			return result, err
		}
		result.Stories = append(result.Stories, story)
	}
	return result, rows.Err()
}

func scanStory(row pgx.Row) (model.Story, error) {
	var story model.Story
	err := row.Scan(&story.ID, &story.Rubric, &story.Topic, &story.CreatedAt, &story.PublishedAt, &story.TrustAtPublish)
	return story, err
}

var storyColumns = []string{
	"id",
	"rubric",
	"topic",
	"created_at",
	"published_at",
	"trust_at_publish",
}

func (s *Service) Stories(ctx context.Context, limit int) ([]model.Story, error) {
	if s.cache != nil {
		if result, ok := s.cache.Stories(limit); ok {
			return result, nil
		}
	}
	if limit < 1 || limit > 1000 {
		return nil, model.NewError("invalid", "limit must be between 1 and 1000")
	}
	tx, err := s.db.BeginTx(ctx, pgx.TxOptions{
		IsoLevel:   pgx.RepeatableRead,
		AccessMode: pgx.ReadOnly,
	})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	query, args, err := psql.Select(storyColumns...).
		From("stories").
		OrderBy("id DESC").
		Limit(uint64(limit)).
		ToSql()
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	result := make([]model.Story, 0)
	for rows.Next() {
		story, err := scanStory(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		result = append(result, story)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	now := s.cfg.Now().UTC()
	for i := range result {
		if err = s.fillStory(ctx, tx, &result[i], now, false); err != nil {
			return nil, err
		}
	}
	return result, tx.Commit(ctx)
}

func (s *Service) Story(ctx context.Context, id int64) (model.Story, error) {
	if s.cache != nil {
		if result, ok := s.cache.Story(id); ok {
			return result, nil
		}
	}
	tx, err := s.db.BeginTx(ctx, pgx.TxOptions{
		IsoLevel:   pgx.RepeatableRead,
		AccessMode: pgx.ReadOnly,
	})
	if err != nil {
		return model.Story{}, err
	}
	defer tx.Rollback(ctx)
	query, args, err := psql.Select(storyColumns...).
		From("stories").
		Where(sq.Eq{"id": id}).
		ToSql()
	if err != nil {
		return model.Story{}, err
	}
	story, err := scanStory(tx.QueryRow(ctx, query, args...))
	if errors.Is(err, pgx.ErrNoRows) {
		return story, model.NewError("not_found", "story not found")
	}
	if err != nil {
		return story, err
	}
	if err = s.fillStory(ctx, tx, &story, s.cfg.Now().UTC(), true); err != nil {
		return story, err
	}
	return story, tx.Commit(ctx)
}

func (s *Service) fillStory(ctx context.Context, tx pgx.Tx, story *model.Story, now time.Time, participants bool) error {
	if story.TrustAtPublish != nil {
		story.CurrentTrust = *story.TrustAtPublish
	} else {
		total, err := s.storyTrust(ctx, tx, story.ID, now)
		if err != nil {
			return err
		}
		story.CurrentTrust = total
	}
	if !participants {
		return nil
	}
	query, args, err := psql.Select("n.client_id", "c.name", "n.id", "n.received_at", "n.rank").
		From("notes n").
		Join("clients c ON c.id = n.client_id").
		Where(sq.Eq{"n.story_id": story.ID}).
		Where("n.is_contribution").
		OrderBy("n.id").
		ToSql()
	if err != nil {
		return err
	}
	rows, err := tx.Query(ctx, query, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	story.Participants = make([]model.Participant, 0)
	for rows.Next() {
		var p model.Participant
		if err = rows.Scan(&p.ClientID, &p.Name, &p.FirstNoteID, &p.ReceivedAt, &p.Rank); err != nil {
			return err
		}
		story.Participants = append(story.Participants, p)
	}
	return rows.Err()
}
