package cli

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/bochkarevart/1c-selection/internal/model"
	"github.com/spf13/cobra"
)

func (a *app) noteCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "note",
		Short: "Send and retry notes",
	}
	var rubric, topic, body, eventAt string
	send := &cobra.Command{
		Use:   "send",
		Short: "Persist a new note locally, then send it",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, err := a.connect(cmd.Context())
			if err != nil {
				return err
			}
			if strings.TrimSpace(rubric) == "" || strings.TrimSpace(topic) == "" {
				return errors.New("--rubric and --topic are required")
			}
			at := a.now().UTC()
			if eventAt != "" {
				at, err = time.Parse(time.RFC3339Nano, eventAt)
				if err != nil {
					return errors.New("--event-at must be RFC3339, for example 2026-10-18T12:00:00+03:00")
				}
			}
			id, err := newRequestID()
			if err != nil {
				return err
			}
			note := pendingNote{
				Server:   a.server,
				ClientID: client.ID,
				SavedAt:  a.now().UTC(),
				Note: model.NoteRequest{
					RequestID: id,
					Rubric:    rubric,
					Topic:     topic,
					Body:      body,
					EventAt:   at,
				},
			}
			path, err := a.pendingPath(id)
			if err != nil {
				return err
			}
			if err := privateDirectory(a.stateDir); err != nil {
				return err
			}
			if err := atomicJSON(path, note); err != nil {
				return fmt.Errorf("save pending note: %w", err)
			}
			return a.sendPending(cmd, client, note)
		},
	}
	send.Flags().StringVar(&rubric, "rubric", "", "Exact story rubric")
	send.Flags().StringVar(&topic, "topic", "", "Exact story topic")
	send.Flags().StringVar(&body, "body", "", "Note text")
	send.Flags().StringVar(&eventAt, "event-at", "", "Event time in RFC3339 (default: now)")
	cmd.AddCommand(send)
	cmd.AddCommand(&cobra.Command{
		Use:   "pending",
		Short: "List saved notes with their server and author bindings",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			notes, err := a.pending()
			if err != nil {
				return err
			}
			return printJSON(cmd.OutOrStdout(), notes)
		},
	})
	var all bool
	retry := &cobra.Command{
		Use:   "retry [request-id]",
		Short: "Retry the exact saved note; --all retries this account's outbox",
		Args: func(_ *cobra.Command, args []string) error {
			if (all && len(args) == 0) || (!all && len(args) == 1) {
				return nil
			}
			return errors.New("specify one request ID or --all")
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := a.connect(cmd.Context())
			if err != nil {
				return err
			}
			if !all {
				path, err := a.pendingPath(args[0])
				if err != nil {
					return err
				}
				var note pendingNote
				if err := readJSON(path, &note); err != nil {
					return fmt.Errorf("read pending note: %w", err)
				}
				if note.Note.RequestID != args[0] {
					return errors.New("saved note request ID does not match its file")
				}
				return a.sendPending(cmd, client, note)
			}
			notes, err := a.pending()
			if err != nil {
				return err
			}
			var retryErrors []error
			for _, note := range notes {
				if note.Server == a.server && note.ClientID == client.ID {
					if err := a.sendPending(cmd, client, note); err != nil {
						retryErrors = append(retryErrors, err)
					}
				}
			}
			return errors.Join(retryErrors...)
		},
	}
	retry.Flags().BoolVar(&all, "all", false, "Retry all notes belonging to the current server and account")
	cmd.AddCommand(retry)
	return cmd
}

func (a *app) sendPending(cmd *cobra.Command, client model.Client, note pendingNote) error {
	if note.Server != a.server || note.ClientID != client.ID {
		return errors.New("pending note belongs to another server or account; log in as its original author on the original server")
	}
	var receipt model.Receipt
	if err := a.authenticatedRequest(cmd.Context(), http.MethodPost, "/notes", note.Note, &receipt); err != nil {
		return fmt.Errorf("%w; note %s remains saved, retry with: times note retry %s", err, note.Note.RequestID, note.Note.RequestID)
	}
	if receipt.Status != "accepted" && receipt.Status != "duplicate" {
		return fmt.Errorf("unexpected receipt status %q; note %s remains saved", receipt.Status, note.Note.RequestID)
	}
	if err := printJSON(cmd.OutOrStdout(), struct {
		RequestID string `json:"request_id"`
		model.Receipt
	}{note.Note.RequestID, receipt}); err != nil {
		return err
	}
	path, err := a.pendingPath(note.Note.RequestID)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("receipt received, but could not remove pending note: %w", err)
	}
	return syncDirectory(filepath.Dir(path))
}
