package cli

import (
	"strconv"

	"github.com/bochkarevart/1c-selection/internal/model"
	"github.com/spf13/cobra"
)

func (a *app) statsCommand() *cobra.Command {
	var all bool
	cmd := &cobra.Command{
		Use:   "stats",
		Short: "Show personal statistics, or --all for the editor",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if all {
				var stats []model.Stats
				return a.getAndPrint(cmd, "/stats", &stats)
			}
			var stats model.Stats
			return a.getAndPrint(cmd, "/stats/me", &stats)
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "Show all clients' statistics (editor only)")
	return cmd
}

func (a *app) storiesCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "stories [story-id]",
		Short: "List stories or show participants of one story",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				var stories []model.Story
				return a.getAndPrint(cmd, "/stories", &stories)
			}
			id, err := positiveID(args[0])
			if err != nil {
				return err
			}
			var story model.Story
			return a.getAndPrint(cmd, "/stories/"+strconv.FormatInt(id, 10), &story)
		},
	}
}
