// Command deepcode serves the UI, API and background indexing from one process.
package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"deepcode/cmd/deepcode/config"
	"deepcode/internal/app"
	"deepcode/internal/platform/storage"
)

// version is set by a release build (scripts/build.sh with VERSION): the tag,
// or dev-<commit> for a build of main.
var version = "dev"

func main() {
	root := &cobra.Command{
		Use:     "deepcode",
		Version: version,
		Short:   "A code wiki: repositories indexed, written up and asked about",
		Long: `deepcode serves the UI, API and background indexing from one process.

Settings come from deepcode.yaml (or the file DEEPCODE_CONFIG names) and
DEEPCODE_* environment variables, which override it; flags override both.
See README.md.`,
		// Wrong flags or arguments show the usage; a command that fails, its error alone.
		PersistentPreRun: func(cmd *cobra.Command, _ []string) { cmd.SilenceUsage = true },
		SilenceErrors:    true,
	}
	root.AddCommand(serveCmd(), backupCmd(), passwdCmd(), importSCIPCmd())
	if err := root.Execute(); err != nil {
		log.Fatal(err)
	}
}

// dataFlag adds --data to cmd: the data directory, in place of the configured
// one. Given it, backup and passwd do not read the config at all.
func dataFlag(cmd *cobra.Command) *string {
	return cmd.Flags().String("data", "", "data directory (default: the configured one)")
}

// dataPath resolves the database path in the data directory dir;
// dir "" is the configured one. Given --data, the config is not read at all,
// so a broken one cannot block a backup or a password reset.
func dataPath(dir string) (string, error) {
	if dir == "" {
		var err error
		if dir, err = config.DataDir(); err != nil {
			return "", err
		}
	}
	return filepath.Join(dir, "deepcode.db"), nil
}

func openData(dir string) (*sql.DB, error) {
	path, err := dataPath(dir)
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(path); err != nil {
		return nil, err
	}
	return storage.Open(context.Background(), path)
}

func importSCIPCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "import-scip <manifest.json> <index.scip>",
		Short: "Import a trusted Go SCIP artifact into an already indexed snapshot",
		Args:  cobra.ExactArgs(2),
	}
	data := dataFlag(cmd)
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		manifest, err := os.Open(args[0])
		if err != nil {
			return err
		}
		defer manifest.Close()
		index, err := os.Open(args[1])
		if err != nil {
			return err
		}
		defer index.Close()

		dir := *data
		if dir == "" {
			dir, err = config.DataDir()
			if err != nil {
				return err
			}
		}
		db, err := openData(dir)
		if err != nil {
			return err
		}
		defer db.Close()
		count, err := app.ImportSCIP(cmd.Context(), db, dir, manifest, index)
		if err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Imported %d Go SCIP occurrences.\n", count)
		return nil
	}
	return cmd
}
