package cli

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/bochkarevart/1c-selection/internal/model"
	"github.com/spf13/cobra"
)

func (a *app) notificationsCommand() *cobra.Command {
	var noAck bool
	var limit int
	cmd := &cobra.Command{
		Use:   "notifications",
		Short: "Print pending notifications, then acknowledge them",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if limit < 1 || limit > 1000 {
				return errors.New("--limit must be between 1 and 1000")
			}
			_, err := a.connect(cmd.Context())
			if err != nil {
				return err
			}
			var notifications []model.Notification
			if err := a.authenticatedRequest(cmd.Context(), http.MethodGet, "/notifications?limit="+strconv.Itoa(limit), nil, &notifications); err != nil {
				return err
			}
			if err := printJSON(cmd.OutOrStdout(), notifications); err != nil {
				return err
			}
			if !noAck {
				return a.ackNotifications(cmd.Context(), notifications)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&noAck, "no-ack", false, "Leave printed notifications unacknowledged")
	cmd.Flags().IntVar(&limit, "limit", 100, "Maximum number of notifications")
	return cmd
}

func (a *app) ackNotifications(ctx context.Context, notifications []model.Notification) error {
	for _, notification := range notifications {
		path := "/notifications/" + strconv.FormatInt(notification.ID, 10) + "/ack"
		if err := a.authenticatedRequest(ctx, http.MethodPost, path, nil, nil); err != nil {
			return fmt.Errorf("acknowledge notification %d: %w", notification.ID, err)
		}
	}
	return nil
}
