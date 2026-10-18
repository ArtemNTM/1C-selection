package cli

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/bochkarevart/1c-selection/internal/model"
	"github.com/spf13/cobra"
)

func (a *app) clientsCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "clients",
		Short: "Manage clients (editor only)",
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List clients",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			var clients []model.Client
			return a.getAndPrint(cmd, "/clients", &clients)
		},
	})
	var create model.CreateClientRequest
	createCmd := &cobra.Command{
		Use:   "create",
		Short: "Create a client and print its one-time personal token",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if strings.TrimSpace(create.Name) == "" {
				return errors.New("--name is required")
			}
			if create.Kind != "staff" && create.Kind != "freelance" {
				return errors.New("--kind must be staff or freelance")
			}
			if create.Role != "reporter" && create.Role != "editor" {
				return errors.New("--role must be reporter or editor")
			}
			if create.Kind == "staff" && !cmd.Flags().Changed("trust") {
				create.Trust = 1
			}
			_, err := a.connect(cmd.Context())
			if err != nil {
				return err
			}
			var credentials model.ClientCredentials
			if err := a.authenticatedRequest(cmd.Context(), http.MethodPost, "/clients", create, &credentials); err != nil {
				return err
			}
			return printJSON(cmd.OutOrStdout(), credentials)
		},
	}
	createCmd.Flags().StringVar(&create.Name, "name", "", "Client name")
	createCmd.Flags().StringVar(&create.Kind, "kind", "freelance", "staff or freelance")
	createCmd.Flags().StringVar(&create.Role, "role", "reporter", "reporter or editor")
	createCmd.Flags().Float64Var(&create.Trust, "trust", 0.25, "Trust between 0 and 1 (default: staff 1, freelance 0.25)")
	cmd.AddCommand(createCmd)
	var trust float64
	trustCmd := &cobra.Command{
		Use:   "trust <client-id>",
		Short: "Update a client's trust",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := positiveID(args[0])
			if err != nil {
				return err
			}
			if !cmd.Flags().Changed("trust") {
				return errors.New("--trust is required")
			}
			_, err = a.connect(cmd.Context())
			if err != nil {
				return err
			}
			var updated model.Client
			path := "/clients/" + strconv.FormatInt(id, 10)
			request := model.UpdateClientRequest{Trust: &trust}
			if err := a.authenticatedRequest(cmd.Context(), http.MethodPatch, path, request, &updated); err != nil {
				return err
			}
			return printJSON(cmd.OutOrStdout(), updated)
		},
	}
	trustCmd.Flags().Float64Var(&trust, "trust", 0, "Trust between 0 and 1")
	cmd.AddCommand(trustCmd)
	var enabled bool
	enabledCmd := &cobra.Command{
		Use:   "enabled <client-id>",
		Short: "Enable or disable a client",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := positiveID(args[0])
			if err != nil {
				return err
			}
			if !cmd.Flags().Changed("enabled") {
				return errors.New("--enabled is required")
			}
			if _, err := a.connect(cmd.Context()); err != nil {
				return err
			}
			var updated model.Client
			path := "/clients/" + strconv.FormatInt(id, 10)
			request := model.UpdateClientRequest{Enabled: &enabled}
			if err := a.authenticatedRequest(cmd.Context(), http.MethodPatch, path, request, &updated); err != nil {
				return err
			}
			return printJSON(cmd.OutOrStdout(), updated)
		},
	}
	enabledCmd.Flags().BoolVar(&enabled, "enabled", true, "Whether the client may authenticate")
	cmd.AddCommand(enabledCmd)
	return cmd
}
