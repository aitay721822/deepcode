// Command deepcode serves the UI, API and background indexing from one process.
package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"deepcode/internal/platform/storage"
)

const usage = `usage:
  deepcode serve  [-addr 127.0.0.1:8080] [-data ./data]
  deepcode backup [-data ./data] <output.db>
  deepcode passwd [-data ./data] <username>   (reads the new password from stdin)

Configuration comes from DEEPCODE_* environment variables; see README.md.`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "serve":
		err = serve(os.Args[2:])
	case "backup":
		err = backup(os.Args[2:])
	case "passwd":
		err = passwd(os.Args[2:])
	default:
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(2)
	}
	if err != nil {
		log.Fatal(err)
	}
}

// openData opens the database in the data directory dir, which must exist.
func openData(dir string) (*sql.DB, error) {
	path := filepath.Join(dir, "deepcode.db")
	if _, err := os.Stat(path); err != nil {
		return nil, err
	}
	return storage.Open(context.Background(), path)
}
