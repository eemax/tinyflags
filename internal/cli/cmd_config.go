package cli

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/spf13/cobra"

	cerr "github.com/eemax/tinyflags/internal/errors"
)

func (a *App) newConfigCommand(globals *globalOptions) *cobra.Command {
	cmd := &cobra.Command{Use: "config", Short: "Config inspection"}
	cmd.AddCommand(&cobra.Command{
		Use:           "show",
		Short:         "Show resolved config",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, _, err := a.loadCommandConfig(globals.configPath)
			if err != nil {
				return err
			}
			cfg.APIKey = redactKey(cfg.APIKey)
			return a.renderValue(globals.format, cfg, func() string {
				data, _ := json.MarshalIndent(cfg, "", "  ")
				return string(data)
			})
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:           "path",
		Short:         "Show config path",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			_, path, err := a.loadCommandConfig(globals.configPath)
			if err != nil {
				return err
			}
			return a.renderValue(globals.format, map[string]any{"path": path}, func() string { return path })
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:           "validate",
		Short:         "Validate config",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, path, err := a.loadCommandConfig(globals.configPath)
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), 10*time.Second)
			defer cancel()
			report, err := validateConfigModels(ctx, cfg, a.HTTPClient)
			if err != nil {
				return err
			}
			if report.HasFailures() {
				return cerr.New(cerr.ExitRuntime, strings.Join(report.FailureMessages(), "; "))
			}
			payload := map[string]any{"ok": true, "path": path, "default_mode": cfg.DefaultMode}
			if len(report.Checks) > 0 {
				payload["openrouter_validation"] = report.Checks
			}
			if len(report.Warnings) > 0 {
				payload["warnings"] = report.Warnings
			}
			return a.renderValue(globals.format, payload, func() string {
				if len(report.Warnings) == 0 {
					return "ok"
				}
				lines := []string{"ok"}
				for _, warning := range report.Warnings {
					lines = append(lines, "warning: "+warning)
				}
				return strings.Join(lines, "\n")
			})
		},
	})
	return cmd
}
