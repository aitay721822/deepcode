package main

import (
	"testing"

	"github.com/spf13/cobra"
)

// TestFrozenCommands: the commands, arguments and flags v1.0.0 has are still
// there (README's compatibility promise): a release may add commands and
// flags, never drop or rename one of these.
func TestFrozenCommands(t *testing.T) {
	cmds := map[string]*cobra.Command{}
	for _, c := range []*cobra.Command{serveCmd(), backupCmd(), passwdCmd()} {
		cmds[c.Name()] = c
	}
	args := map[string][]string{"serve": nil, "backup": {"backup.db"}, "passwd": {"admin"}}
	for name, flags := range map[string][]string{"serve": {"addr", "data"}, "backup": {"data"}, "passwd": {"data"}} {
		c := cmds[name]
		if c == nil {
			t.Errorf("v1.0.0's command %s is gone", name)
			continue
		}
		if err := c.ValidateArgs(args[name]); err != nil {
			t.Errorf("v1.0.0's %s %v: %v", name, args[name], err)
		}
		for _, f := range flags {
			if c.Flags().Lookup(f) == nil {
				t.Errorf("v1.0.0's flag %s --%s is gone", name, f)
			}
		}
	}
}
