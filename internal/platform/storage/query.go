package storage

import "strings"

// Placeholders returns "?,?,...,?" with n markers for IN clauses.
func Placeholders(n int) string {
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}
