package cli

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/spf13/cobra"
)

func positiveID(raw string) (int64, error) {
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		return 0, errors.New("ID must be a positive integer")
	}
	return id, nil
}

func (a *app) getAndPrint(cmd *cobra.Command, path string, result any) error {
	_, err := a.connect(cmd.Context())
	if err != nil {
		return err
	}
	if err := a.authenticatedRequest(cmd.Context(), http.MethodGet, path, nil, result); err != nil {
		return err
	}
	return printJSON(cmd.OutOrStdout(), result)
}
