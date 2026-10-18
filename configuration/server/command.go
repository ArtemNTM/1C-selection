package server

import (
	"context"

	"github.com/spf13/cobra"
)

func NewCommand(run func(context.Context, Config) error) *cobra.Command {
	overrides := defaultConfig()
	var cfg Config
	var configPath string
	root := &cobra.Command{
		Use:           "times-server",
		Short:         "Newsroom server",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			var err error
			cfg, err = loadConfig(configPath, cmd.Flags(), overrides)
			return err
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			return run(cmd.Context(), cfg)
		},
	}
	flags := root.PersistentFlags()
	flags.StringVar(&configPath, "config", defaultConfigPath, "YAML configuration file")
	flags.StringVar(&overrides.Addr, "addr", overrides.Addr, "HTTP listen address")
	flags.StringVar(&overrides.DatabaseURL, "database-url", overrides.DatabaseURL, "PostgreSQL connection URL")
	flags.Float64Var(&overrides.Threshold, "threshold", overrides.Threshold, "Trust threshold required to publish a story")
	flags.DurationVar(&overrides.HalfLife, "half-life", overrides.HalfLife, "Time for a note's trust to decrease by half")
	flags.IntVar(&overrides.Congratulate, "congratulate", overrides.Congratulate, "Number of first contributors to congratulate")
	flags.DurationVar(&overrides.CacheRefresh, "cache-refresh", overrides.CacheRefresh, "Interval between cache refreshes")
	flags.DurationVar(&overrides.SessionTTL, "session-ttl", overrides.SessionTTL, "Session lifetime after a successful handshake")
	return root
}
