package cli

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/bochkarevart/1c-selection/internal/model"
	"github.com/spf13/cobra"
)

func (a *app) connect(ctx context.Context) (model.Client, error) {
	if a.client != nil && a.sessionToken != "" && a.now().Before(a.expiresAt) {
		return *a.client, nil
	}
	if strings.TrimSpace(a.clientID) == "" || a.token == "" {
		return model.Client{}, errors.New("client_id and token are required; set the token with --token, TIMES_CLI_TOKEN, a token file, or YAML")
	}
	var response model.HelloResponse
	if err := a.request(ctx, http.MethodPost, "/hello", "", model.HelloRequest{
		ClientID: a.clientID,
		Token:    a.token,
	}, &response); err != nil {
		return model.Client{}, err
	}
	if response.Client.ID <= 0 || response.Client.Name != a.clientID || !response.Client.Enabled {
		return model.Client{}, errors.New("server returned an invalid client identity")
	}
	if a.client != nil && response.Client.ID != a.client.ID {
		return model.Client{}, errors.New("server returned a different client identity while renewing the session")
	}
	if response.SessionToken == "" || len(response.SessionToken) > 512 ||
		strings.IndexFunc(response.SessionToken, func(r rune) bool { return r <= ' ' || r > '~' }) >= 0 ||
		!response.ExpiresAt.After(a.now()) {
		return model.Client{}, errors.New("server returned an invalid session")
	}
	a.client = &response.Client
	a.sessionToken, a.expiresAt = response.SessionToken, response.ExpiresAt
	return response.Client, nil
}

func (a *app) connectCommand() *cobra.Command {
	var noAck bool
	cmd := &cobra.Command{
		Use:   "connect",
		Short: "Handshake and receive pending notifications",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, err := a.connect(cmd.Context())
			if err != nil {
				return err
			}
			const limit = 1000
			for {
				var notifications []model.Notification
				if err := a.authenticatedRequest(cmd.Context(), http.MethodGet, "/notifications?limit="+strconv.Itoa(limit), nil, &notifications); err != nil {
					return err
				}
				if notifications == nil {
					notifications = []model.Notification{}
				}
				if err := printJSON(cmd.OutOrStdout(), struct {
					Client        model.Client         `json:"client"`
					Notifications []model.Notification `json:"notifications"`
				}{client, notifications}); err != nil {
					return err
				}
				if noAck {
					return nil
				}
				if err := a.ackNotifications(cmd.Context(), notifications); err != nil {
					return err
				}
				if len(notifications) < limit {
					return nil
				}
			}
		},
	}
	cmd.Flags().BoolVar(&noAck, "no-ack", false, "Receive one batch without acknowledging notifications")
	return cmd
}
