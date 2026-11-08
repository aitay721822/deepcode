package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"deepcode/internal/platform/storage"
)

func backupCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "backup <output.db>",
		Short: "Write a consistent copy of the database, safe while serve runs",
		Args:  cobra.ExactArgs(1),
	}
	data := dataFlag(cmd)
	cmd.RunE = func(_ *cobra.Command, args []string) error { return backup(*data, args[0]) }
	return cmd
}

// backup writes a consistent copy of the database to out.
func backup(data, out string) error {
	db, err := openData(data)
	if err != nil {
		return err
	}
	defer db.Close()
	// Ctrl+C or a stop interrupts VACUUM INTO, and Backup removes the unfinished
	// file, which would otherwise wait at the backup's name looking like one.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := storage.Backup(ctx, db, out); err != nil {
		return err
	}
	log.Printf("backup written to %s", out)
	return nil
}
