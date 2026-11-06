package main

import (
	"context"
	"errors"
	"flag"
	"log"

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
	if err := storage.Backup(context.Background(), db, fs.Arg(0)); err != nil {
		return err
	}
	log.Printf("backup written to %s", fs.Arg(0))
	return nil
}
