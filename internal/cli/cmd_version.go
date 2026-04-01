package cli

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/eemax/tinyflags/internal/version"
)

func (a *App) newVersionCommand() *cobra.Command {
	return &cobra.Command{
		Use:           "version",
		Short:         "Print version/build info",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			_ = args
			payload := map[string]string{"version": version.Version, "commit": version.Commit, "date": version.Date}
			format := currentFormat(cmd)
			if err := validateCLIFormat(format); err != nil {
				return err
			}
			if format == "json" {
				enc := json.NewEncoder(a.Stdout)
				enc.SetEscapeHTML(false)
				return enc.Encode(payload)
			}
			_, err := fmt.Fprintf(a.Stdout, "%s %s %s\n", version.Version, version.Commit, version.Date)
			return err
		},
	}
}
