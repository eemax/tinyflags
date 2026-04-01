package cli

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/eemax/tinyflags/internal/provider/openrouter"
	"github.com/eemax/tinyflags/internal/store"
)

func (a *App) newDoctorCommand(globals *globalOptions) *cobra.Command {
	return &cobra.Command{
		Use:           "doctor",
		Short:         "Environment checks",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, path, err := a.loadCommandConfig(globals.configPath)
			if err != nil {
				return err
			}
			checks := map[string]any{
				"config_path":  path,
				"config_parse": map[string]any{"ok": true},
				"api_key":      map[string]any{"ok": cfg.APIKey != ""},
			}
			db, dbErr := store.OpenDB(cfg.DBPath)
			if dbErr != nil {
				checks["db"] = map[string]any{"ok": false, "error": dbErr.Error()}
			} else {
				defer db.Close()
				checks["db"] = map[string]any{"ok": true}
			}
			if info, err := os.Stat(cfg.SkillsDir); err != nil {
				checks["skills_dir"] = map[string]any{"ok": false, "error": err.Error()}
			} else {
				checks["skills_dir"] = map[string]any{"ok": info.IsDir()}
			}
			if resolved, err := exec.LookPath(cfg.Shell); err != nil {
				checks["shell"] = map[string]any{"ok": false, "error": err.Error()}
			} else {
				checks["shell"] = map[string]any{"ok": true, "path": resolved}
			}
			if cfg.APIKey == "" {
				checks["openrouter"] = map[string]any{"ok": false, "error": "missing API key"}
			} else {
				ctx, cancel := context.WithTimeout(cmd.Context(), 10*time.Second)
				defer cancel()
				if err := openrouter.CheckConnectivity(ctx, cfg.BaseURL, cfg.APIKey, a.HTTPClient); err != nil {
					checks["openrouter"] = map[string]any{"ok": false, "error": err.Error()}
				} else {
					checks["openrouter"] = map[string]any{"ok": true}
				}
			}
			validateCtx, cancel := context.WithTimeout(cmd.Context(), 10*time.Second)
			defer cancel()
			report, err := validateConfigModels(validateCtx, cfg, a.HTTPClient)
			if err != nil {
				checks["openrouter_model_validation"] = map[string]any{"ok": false, "error": err.Error()}
			} else {
				item := map[string]any{"ok": !report.HasFailures()}
				if len(report.Checks) > 0 {
					item["checks"] = report.Checks
				}
				if len(report.Warnings) > 0 {
					item["warnings"] = report.Warnings
				}
				checks["openrouter_model_validation"] = item
			}
			return a.renderValue(globals.format, checks, func() string {
				lines := []string{}
				keys := make([]string, 0, len(checks))
				for name := range checks {
					keys = append(keys, name)
				}
				sort.Strings(keys)
				for _, name := range keys {
					raw := checks[name]
					if name == "config_path" {
						lines = append(lines, fmt.Sprintf("%s: %v", name, raw))
						continue
					}
					item := raw.(map[string]any)
					status := "ok"
					if ok, _ := item["ok"].(bool); !ok {
						status = "fail"
					}
					line := fmt.Sprintf("%s: %s", name, status)
					if err, ok := item["error"].(string); ok && err != "" {
						line += " (" + err + ")"
					} else if warnings, ok := item["warnings"].([]string); ok && len(warnings) > 0 {
						line += " (warning: " + strings.Join(warnings, "; ") + ")"
					} else if warnings, ok := item["warnings"].([]any); ok && len(warnings) > 0 {
						values := make([]string, 0, len(warnings))
						for _, warning := range warnings {
							if text, ok := warning.(string); ok && text != "" {
								values = append(values, text)
							}
						}
						if len(values) > 0 {
							line += " (warning: " + strings.Join(values, "; ") + ")"
						}
					}
					lines = append(lines, line)
				}
				return strings.Join(lines, "\n")
			})
		},
	}
}
