package cli

import (
	"net/http"

	client "github.com/bochkarevart/1c-selection/internal/cli"
	"github.com/spf13/cobra"
)

func NewCommand() *cobra.Command {
	httpClient := &http.Client{
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	overrides := defaultConfig()
	var configPath string
	root := client.NewCommand(httpClient, func(cmd *cobra.Command) (client.Config, error) {
		cfg, err := loadConfig(configPath, cmd.Flags(), overrides)
		return cfg.clientConfig(), err
	})
	flags := root.PersistentFlags()
	flags.StringVar(&configPath, "config", defaultConfigPath, "YAML configuration file")
	flags.StringVar(&overrides.Server, "server", overrides.Server, "Server URL")
	flags.StringVar(&overrides.StateDir, "state-dir", overrides.StateDir, "Private directory for unsent notes")
	flags.DurationVar(&overrides.RequestTimeout, "request-timeout", overrides.RequestTimeout, "HTTP request timeout")
	flags.StringVar(&overrides.ClientID, "client-id", overrides.ClientID, "Client identifier issued by the newsroom")
	flags.StringVar(&overrides.Token, "token", overrides.Token, "Personal client token (overrides TIMES_CLI_TOKEN, token file and YAML)")
	flags.StringVar(&overrides.TokenFile, "token-file", overrides.TokenFile, "File containing the personal token")
	return root
}
