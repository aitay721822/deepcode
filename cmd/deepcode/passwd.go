package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"deepcode/internal/access/account"
	"deepcode/internal/access/account/store"
)

func passwdCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "passwd <username>",
		Short: "Set a user's password from the first line of stdin and end their sessions",
		Args:  cobra.ExactArgs(1),
	}
	data := dataFlag(cmd)
	cmd.RunE = func(_ *cobra.Command, args []string) error { return passwd(*data, args[0]) }
	return cmd
}

// passwd sets username's password from the first line of stdin and ends their
// sessions: the way back in when an administrator's password is lost. Safe
// while serve runs.
func passwd(data, username string) error {
	db, err := openData(data)
	if err != nil {
		return err
	}
	defer db.Close()
	ctx := context.Background()
	accounts := &account.Service{Store: &store.Store{DB: db}}
	u, err := accounts.ByUsername(ctx, username)
	if errors.Is(err, account.ErrNotFound) {
		return fmt.Errorf("there is no user %q", username)
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
	log.Printf("changed the password of %s and ended their sessions", username)
	return nil
}
