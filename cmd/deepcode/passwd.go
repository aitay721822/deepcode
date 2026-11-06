package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"

	"deepcode/internal/access/account"
)

// passwd sets a user's password from the first line of stdin and ends their
// sessions: the way back in when an administrator's password is lost. Safe
// while serve runs.
func passwd(args []string) error {
	fs := flag.NewFlagSet("passwd", flag.ExitOnError)
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
	ctx := context.Background()
	accounts := &account.Store{DB: db}
	u, err := accounts.ByUsername(ctx, fs.Arg(0))
	if errors.Is(err, account.ErrNotFound) {
		return fmt.Errorf("there is no user %q", fs.Arg(0))
	} else if err != nil {
		return err
	}
	fmt.Fprint(os.Stderr, "New password: ")
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && line == "" {
		return err
	}
	password := strings.TrimRight(line, "\r\n")
	if _, err := accounts.Update(ctx, u.ID, 0, account.Input{Password: &password}); err != nil {
		return err
	}
	log.Printf("changed the password of %s and ended their sessions", fs.Arg(0))
	return nil
}
