package cli

import (
	"encoding/json"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/eemax/tinyflags/internal/core"
	"github.com/eemax/tinyflags/internal/mode"
)

func (a *App) newModeCommand(globals *globalOptions) *cobra.Command {
	cmd := &cobra.Command{Use: "mode", Short: "Mode inspection"}
	cmd.AddCommand(&cobra.Command{
		Use:           "list",
		Short:         "List modes",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, _, err := a.loadCommandConfig(globals.configPath)
			if err != nil {
				return err
			}
			names := make([]string, 0, len(cfg.Modes))
			for name := range cfg.Modes {
				names = append(names, name)
			}
			sort.Strings(names)
			return a.renderValue(globals.format, names, func() string { return strings.Join(names, "\n") })
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:           "show <name>",
		Args:          cobra.ExactArgs(1),
		Short:         "Show a resolved mode",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, _, err := a.loadCommandConfig(globals.configPath)
			if err != nil {
				return err
			}
			resolved, err := mode.Resolve(cfg, core.RuntimeRequest{ModeName: args[0], MaxSteps: -1, MaxToolRetries: -1})
			if err != nil {
				return err
			}
			return a.renderValue(globals.format, resolved, func() string {
				data, _ := json.MarshalIndent(resolved, "", "  ")
				return string(data)
			})
		},
	})
	return cmd
}
