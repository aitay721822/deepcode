// Command deepcode serves the UI, API and background indexing from one process.
package main

import (
	"context"
	"database/sql"
	"log"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"deepcode/cmd/deepcode/config"
	"deepcode/internal/platform/storage"
)

func main() {
	root := &cobra.Command{
		Use:   "deepcode",
		Short: "A code wiki: repositories indexed, written up and asked about",
		Long: `deepcode serves the UI, API and background indexing from one process.

Settings come from deepcode.yaml (or the file DEEPCODE_CONFIG names) and
DEEPCODE_* environment variables, which override it; flags override both.
See README.md.`,
		// Wrong flags or arguments show the usage; a command that fails, its error alone.
		PersistentPreRun: func(cmd *cobra.Command, _ []string) { cmd.SilenceUsage = true },
		SilenceErrors:    true,
	}
	root.AddCommand(serveCmd(), backupCmd(), passwdCmd())
	if err := root.Execute(); err != nil {
		log.Fatal(err)
	}
}

// dataFlag adds --data to cmd: the data directory, in place of the configured
// one. Given it, backup and passwd do not read the config at all (openData).
func dataFlag(cmd *cobra.Command) *string {
	return cmd.Flags().String("data", "", "data directory (default: the configured one)")
}

// openData opens the database in the data directory dir, which must exist;
// dir "" is the configured one. Given --data, the config is not read at all,
// so a broken one cannot block a backup or a password reset.
func openData(dir string) (*sql.DB, error) {
	if dir == "" {
		var err error
		if dir, err = config.DataDir(); err != nil {
			return nil, err
		}
	}
	path := filepath.Join(dir, "deepcode.db")
	if _, err := os.Stat(path); err != nil {
		return nil, err
	}
	return storage.Open(context.Background(), path)
}
