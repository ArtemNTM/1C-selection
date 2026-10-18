package service

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"
	"unicode/utf8"

	sq "github.com/Masterminds/squirrel"
	"github.com/bochkarevart/1c-selection/internal/model"
	"github.com/bochkarevart/1c-selection/internal/store"
	"github.com/jackc/pgx/v5"
)

func validateNote(req model.NoteRequest, now time.Time) error {
	if strings.TrimSpace(req.RequestID) == "" || utf8.RuneCountInString(req.RequestID) > 128 {
		return model.NewError("invalid", "request_id must contain 1 to 128 characters")
	}
	if strings.TrimSpace(req.Rubric) == "" ||
		utf8.RuneCountInString(req.Rubric) > 100 ||
		strings.TrimSpace(req.Topic) == "" ||
		utf8.RuneCountInString(req.Topic) > 500 ||
		utf8.RuneCountInString(req.Body) > 20000 {
		return model.NewError("invalid", "rubric (1..100), topic (1..500), and body (up to 20000 characters) are required to fit their limits")
	}
	if strings.ContainsRune(req.RequestID+req.Rubric+req.Topic+req.Body, '\x00') {
		return model.NewError("invalid", "text must not contain NUL characters")
	}
	if req.EventAt.IsZero() || req.EventAt.Year() < 1970 || req.EventAt.After(now.Add(time.Minute)) {
		return model.NewError("invalid", "event_at must be from 1970 onward and no more than one minute in the future")
	}
	return nil
}

func noteHash(req model.NoteRequest) (string, error) {
	req.EventAt = req.EventAt.UTC()
	data, err := json.Marshal(req)
	if err != nil {
		return "", err
	}
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:]), nil
}

func requestLock(clientID int64, requestID string) int64 {
	hash := sha256.Sum256([]byte(fmt.Sprintf("%d:%s", clientID, requestID)))
	return int64(binary.BigEndian.Uint64(hash[:8]))
}

func (s *Service) SubmitNote(ctx context.Context, clientID int64, req model.NoteRequest) (model.Receipt, error) {
	var receipt model.Receipt
	if err := validateNote(req, s.cfg.Now()); err != nil {
		return receipt, err
	}
	hash, err := noteHash(req)
	if err != nil {
		return receipt, err
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return receipt, err
	}
	defer tx.Rollback(ctx)

	query, args, err := psql.Select().
		Column(sq.Expr("pg_advisory_xact_lock(?)", requestLock(clientID, req.RequestID))).
		ToSql()
	if err != nil {
		return receipt, err
	}
	if _, err = tx.Exec(ctx, query, args...); err != nil {
		return receipt, err
	}
	var previousHash string
	query, args, err = psql.Select(
		"n.id",
		"n.story_id",
		"n.is_contribution",
		"s.published_at IS NOT NULL",
		"n.payload_hash",
	).
		From("notes n").
		Join("stories s ON s.id = n.story_id").
		Where(sq.Eq{
			"n.client_id":  clientID,
			"n.request_id": req.RequestID,
		}).
		ToSql()
	if err != nil {
		return receipt, err
	}
	err = tx.QueryRow(ctx, query, args...).Scan(
		&receipt.NoteID,
		&receipt.StoryID,
		&receipt.Contributes,
		&receipt.Published,
		&previousHash,
	)
	if err == nil {
		if previousHash != hash {
			return model.Receipt{}, model.NewError("conflict", "request_id was already used for a different note")
		}
		receipt.Status = "duplicate"
		return receipt, tx.Commit(ctx)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return receipt, err
	}
	var clientExists bool
	query, args, err = psql.Select().
		Column(sq.Expr("EXISTS (?)", sq.Select("1").
			From("clients").
			Where(sq.Eq{"id": clientID}),
		)).
		ToSql()
	if err != nil {
		return receipt, err
	}
	if err = tx.QueryRow(ctx, query, args...).Scan(&clientExists); err != nil {
		return receipt, err
	}
	if !clientExists {
		return receipt, model.NewError("unauthorized", "client not found")
	}
	query, args, err = psql.Insert("stories").
		Columns("rubric", "topic").
		Values(req.Rubric, req.Topic).
		Suffix("ON CONFLICT (rubric, topic) DO NOTHING").
		ToSql()
	if err != nil {
		return receipt, err
	}
	if _, err = tx.Exec(ctx, query, args...); err != nil {
		return receipt, err
	}
	var publishedAt *time.Time

	query, args, err = psql.Select("id", "published_at").
		From("stories").
		Where(sq.Eq{
			"rubric": req.Rubric,
			"topic":  req.Topic,
		}).
		Suffix("FOR UPDATE").
		ToSql()
	if err != nil {
		return receipt, err
	}
	if err = tx.QueryRow(ctx, query, args...).Scan(&receipt.StoryID, &publishedAt); err != nil {
		return receipt, err
	}
	receipt.Published = publishedAt != nil
	if !receipt.Published {
		query, args, err = psql.Select().
			Column(sq.Expr("NOT EXISTS (?)", sq.Select("1").
				From("notes").
				Where(sq.Eq{
					"story_id":  receipt.StoryID,
					"client_id": clientID,
				}).
				Where("is_contribution"),
			)).
			ToSql()
		if err != nil {
			return receipt, err
		}
		if err = tx.QueryRow(ctx, query, args...).Scan(&receipt.Contributes); err != nil {
			return receipt, err
		}
	}
	now := s.cfg.Now().UTC()
	query, args, err = psql.Insert("notes").
		Columns(
			"client_id", "story_id", "request_id", "payload_hash",
			"body", "event_at", "received_at", "is_contribution",
		).
		Values(
			clientID, receipt.StoryID, req.RequestID, hash,
			req.Body, req.EventAt.UTC(), now, receipt.Contributes,
		).
		Suffix("RETURNING id").
		ToSql()
	if err != nil {
		return receipt, err
	}
	if err = tx.QueryRow(ctx, query, args...).Scan(&receipt.NoteID); err != nil {
		return receipt, err
	}
	if !receipt.Published {
		total, err := s.storyTrust(ctx, tx, receipt.StoryID, now)
		if err != nil {
			return receipt, err
		}
		if total >= s.cfg.Threshold {
			if err = s.publish(ctx, tx, receipt.StoryID, now, total); err != nil {
				return receipt, err
			}
			receipt.Published = true
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return model.Receipt{}, err
	}
	receipt.Status = "accepted"
	if s.cache != nil {
		s.cache.InvalidateStory(receipt.StoryID)
	}
	return receipt, nil
}

func decayedTrust(trust float64, eventAt, now time.Time, halfLife time.Duration) float64 {
	age := now.Sub(eventAt)
	if age < 0 {
		age = 0
	}
	return trust * math.Exp2(-float64(age)/float64(halfLife))
}

func (s *Service) storyTrust(ctx context.Context, q store.Executor, storyID int64, now time.Time) (float64, error) {
	query, args, err := psql.Select("c.trust", "n.event_at").
		From("notes n").
		Join("clients c ON c.id = n.client_id").
		Where(sq.Eq{"n.story_id": storyID}).
		Where("n.is_contribution").
		OrderBy("n.id").
		ToSql()
	if err != nil {
		return 0, err
	}
	rows, err := q.Query(ctx, query, args...)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	var total float64
	for rows.Next() {
		var trust float64
		var eventAt time.Time
		if err = rows.Scan(&trust, &eventAt); err != nil {
			return 0, err
		}
		total += decayedTrust(trust, eventAt, now, s.cfg.HalfLife)
	}
	return total, rows.Err()
}

func (s *Service) publish(ctx context.Context, tx pgx.Tx, storyID int64, now time.Time, trust float64) error {
	query, args, err := psql.Update("stories").
		Set("published_at", now).
		Set("trust_at_publish", trust).
		Where(sq.Eq{"id": storyID}).
		ToSql()
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, query, args...); err != nil {
		return err
	}
	ranked := sq.Select("id", "row_number() OVER (ORDER BY id)::integer AS rank").
		From("notes").
		Where(sq.Eq{"story_id": storyID}).
		Where("is_contribution")
	query, args, err = psql.Update("notes n").
		PrefixExpr(sq.Expr("WITH ranked AS (?)", ranked)).
		Set("rank", sq.Expr("r.rank")).
		From("ranked r").
		Where("n.id = r.id").
		ToSql()
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, query, args...); err != nil {
		return err
	}
	notifications := sq.Select("client_id", "story_id", "rank").
		Column(sq.Expr(
			"?::text || rank || ?::text",
			"Поздравляем, ваша заметка попала в печать. Вы были ",
			"-м, кто сказал нам об этой новости.",
		)).
		Column(sq.Expr("?::timestamptz", now)).
		From("notes").
		Where(sq.Eq{"story_id": storyID}).
		Where(sq.LtOrEq{"rank": s.cfg.Congratulate}).
		OrderBy("rank")
	query, args, err = psql.Insert("notifications").
		Columns("client_id", "story_id", "rank", "message", "created_at").
		Select(notifications).
		ToSql()
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, query, args...)
	return err
}
