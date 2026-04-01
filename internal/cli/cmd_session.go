package cli

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/eemax/tinyflags/internal/core"
	"github.com/eemax/tinyflags/internal/session"
	"github.com/eemax/tinyflags/internal/store"
)

func (a *App) newSessionCommand(globals *globalOptions) *cobra.Command {
	cmd := &cobra.Command{Use: "session", Short: "Session management"}
	cmd.AddCommand(&cobra.Command{
		Use:           "list",
		Short:         "List sessions",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			db, admin, err := a.sessionAdmin(globals.configPath)
			if err != nil {
				return err
			}
			defer db.Close()
			items, err := admin.List()
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
		Short:         "Show a session",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			db, admin, err := a.sessionAdmin(globals.configPath)
			if err != nil {
				return err
			}
			defer db.Close()
			sessionValue, messages, err := admin.Show(args[0])
			if err != nil {
				return err
			}
			payload := core.SessionExport{Session: sessionValue, Messages: messages}
			return a.renderValue(globals.format, payload, func() string {
				lines := []string{sessionValue.Name}
				for _, msg := range messages {
					lines = append(lines, fmt.Sprintf("%s: %s", msg.Role, msg.Content))
				}
				return strings.Join(lines, "\n")
			})
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:           "delete <name>",
		Args:          cobra.ExactArgs(1),
		Short:         "Delete a session",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			db, admin, err := a.sessionAdmin(globals.configPath)
			if err != nil {
				return err
			}
			defer db.Close()
			if err := admin.Delete(args[0]); err != nil {
				return err
			}
			return a.renderValue(globals.format, map[string]any{"deleted": args[0]}, func() string { return args[0] })
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:           "clear <name>",
		Args:          cobra.ExactArgs(1),
		Short:         "Clear a session",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			db, admin, err := a.sessionAdmin(globals.configPath)
			if err != nil {
				return err
			}
			defer db.Close()
			if err := admin.Clear(args[0]); err != nil {
				return err
			}
			return a.renderValue(globals.format, map[string]any{"cleared": args[0]}, func() string { return args[0] })
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:           "export <name>",
		Args:          cobra.ExactArgs(1),
		Short:         "Export a session",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			db, admin, err := a.sessionAdmin(globals.configPath)
			if err != nil {
				return err
			}
			defer db.Close()
			exported, err := admin.Export(args[0])
			if err != nil {
				return err
			}
			return a.renderValue(globals.format, exported, func() string {
				data, _ := json.MarshalIndent(exported, "", "  ")
				return string(data)
			})
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:           "fork <source> <destination>",
		Args:          cobra.ExactArgs(2),
		Short:         "Fork a session without running a prompt",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			db, admin, err := a.sessionAdmin(globals.configPath)
			if err != nil {
				return err
			}
			defer db.Close()
			forked, err := admin.Fork(args[0], args[1])
			if err != nil {
				return err
			}
			return a.renderValue(globals.format, forked, func() string { return forked.Name })
		},
	})
	return cmd
}

func (a *App) sessionAdmin(configPath string) (*sql.DB, session.AdminStore, error) {
	cfg, _, err := a.loadCommandConfig(configPath)
	if err != nil {
		return nil, nil, err
	}
	db, err := store.OpenDB(cfg.DBPath)
	if err != nil {
		return nil, nil, err
	}
	return db, session.NewSQLiteStore(db), nil
}
