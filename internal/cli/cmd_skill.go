package cli

import (
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/eemax/tinyflags/internal/skill"
)

func (a *App) newSkillCommand(globals *globalOptions) *cobra.Command {
	cmd := &cobra.Command{Use: "skill", Short: "Skill inspection"}
	cmd.AddCommand(&cobra.Command{
		Use:           "list",
		Short:         "List skills",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, _, err := a.loadCommandConfig(globals.configPath)
			if err != nil {
				return err
			}
			cwd, err := os.Getwd()
			if err != nil {
				return err
			}
			items, err := skill.List(cwd, cfg)
			if err != nil {
				return err
			}
			return a.renderValue(globals.format, items, func() string {
				lines := make([]string, 0, len(items))
				for _, item := range items {
					lines = append(lines, item.Name)
				}
				return strings.Join(lines, "\n")
			})
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:           "show <name>",
		Args:          cobra.ExactArgs(1),
		Short:         "Show a skill",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, _, err := a.loadCommandConfig(globals.configPath)
			if err != nil {
				return err
			}
			cwd, err := os.Getwd()
			if err != nil {
				return err
			}
			content, info, err := skill.Load(args[0], cwd, cfg)
			if err != nil {
				return err
			}
			payload := map[string]any{"name": info.Name, "source": info.Source, "path": info.Path, "content": content}
			return a.renderValue(globals.format, payload, func() string { return content })
		},
	})
	return cmd
}
