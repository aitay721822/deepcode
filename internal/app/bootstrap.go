package app

import (
	"context"
	"fmt"
	"log"

	"deepcode/internal/access/account"
	"deepcode/internal/knowledge/chat"
)

// bootstrapAdmin creates the user "admin" on the first start and says how to
// sign in; the conversations from before there were accounts go to the
// first administrator.
func bootstrapAdmin(ctx context.Context, accounts *account.Service, conversations *chat.Service, password string) error {
	created, generated, err := accounts.Bootstrap(ctx, password)
	switch {
	case err != nil:
		return fmt.Errorf("creating the first administrator (DEEPCODE_ADMIN_PASSWORD): %w", err)
	case created && generated != "":
		log.Printf("created the administrator \"admin\" with the one-time password %s: sign in and change it", generated)
	case created:
		log.Printf("created the administrator \"admin\" with the password in DEEPCODE_ADMIN_PASSWORD")
	}
	admin, err := accounts.FirstAdmin(ctx)
	if err != nil {
		return err
	}
	return conversations.Adopt(ctx, admin)
}
