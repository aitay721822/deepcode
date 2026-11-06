package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"

	"deepcode/internal/platform/storage"
)

// backup writes a consistent copy of the database, safe while serve runs.
func backup(args []string) error {
	fs := flag.NewFlagSet("backup", flag.ExitOnError)
	data := fs.String("data", env("DEEPCODE_DATA", "data"), "data directory")
	fs.Parse(args)
	if fs.NArg() != 1 {
		return errors.New(usage)
	}
	db, err := openData(*data)
	if err != nil {
		return err
	}
	defer db.Close()
	// Ctrl+C or a stop interrupts VACUUM INTO, and Backup removes the unfinished
	// file, which would otherwise wait at the backup's name looking like one.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := storage.Backup(ctx, db, fs.Arg(0)); err != nil {
		return err
	}
	log.Printf("backup written to %s", fs.Arg(0))
	return nil
}
