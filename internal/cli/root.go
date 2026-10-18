package cli

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"time"

	"github.com/bochkarevart/1c-selection/internal/model"
	"github.com/spf13/cobra"
)

type app struct {
	server       string
	stateDir     string
	clientID     string
	token        string
	sessionToken string
	expiresAt    time.Time
	client       *model.Client
	http         *http.Client
	now          func() time.Time
}

func NewCommand(client *http.Client, load func(*cobra.Command) (Config, error)) *cobra.Command {
	a := &app{
		http: client,
		now:  time.Now,
	}
	root := &cobra.Command{
		Use:           "times",
		Short:         "Newsroom client",
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := load(cmd)
			if err != nil {
				return err
			}
			server, err := normalizeServer(cfg.Server)
			if err != nil {
				return err
			}
			a.server = server
			if cfg.StateDir == "" {
				return errors.New("cannot determine configuration directory; specify --state-dir")
			}
			if cfg.RequestTimeout <= 0 {
				return errors.New("request_timeout must be greater than zero")
			}
			a.http.Timeout = cfg.RequestTimeout
			a.clientID, a.token = cfg.ClientID, cfg.Token
			a.client, a.sessionToken, a.expiresAt = nil, "", time.Time{}
			a.stateDir, err = filepath.Abs(cfg.StateDir)
			return err
		},
	}
	root.AddCommand(
		a.connectCommand(),
		a.noteCommand(),
		a.notificationsCommand(),
		a.statsCommand(),
		a.clientsCommand(),
		a.storiesCommand(),
	)
	return root
}

func printJSON(out io.Writer, value any) error {
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	return enc.Encode(value)
}
