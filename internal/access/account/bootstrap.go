package account

import (
	"context"
	"crypto/rand"
	"encoding/base64"

	"deepcode/internal/access/role"
)

// Bootstrap creates the first administrator, "admin", while there are no
// users: with password, or with a generated one it returns when password is
// "".
func (s *Service) Bootstrap(ctx context.Context, password string) (created bool, generated string, err error) {
	if n, err := s.Store.Count(ctx); err != nil || n > 0 {
		return false, "", err
	}
	if password == "" {
		b := make([]byte, 12)
		rand.Read(b)
		generated = base64.RawURLEncoding.EncodeToString(b)
		password = generated
	}
	name, admin := "admin", []int64{role.Admin}
	if _, err := s.Create(ctx, Input{Username: &name, Password: &password, RoleIDs: &admin}); err != nil {
		return false, "", err
	}
	return true, generated, nil
}
