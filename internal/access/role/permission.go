// Package role keeps the roles of role-based access control: each grants a
// set of permissions, either on every repository or on the repositories
// listed for it.
package role

// Permission names one thing a user may do. The repo.read, repo.chat and
// repo.sync permissions (repoScoped) apply to a role's repositories. The
// others are not tied to a role's repositories, though jobs.view shows only
// the jobs of repositories the user can read.
type Permission string

// repoScoped are the permissions a role grants on its repositories only.
var repoScoped = []Permission{RepoRead, RepoChat, RepoSync}

const (
	RepoRead      Permission = "repo.read"
	RepoChat      Permission = "repo.chat"
	RepoImport    Permission = "repo.import"
	RepoSync      Permission = "repo.sync"
	JobsView      Permission = "jobs.view"
	UsersManage   Permission = "users.manage"
	ServersManage Permission = "servers.manage"
)

// Info describes a permission for the admin console.
type Info struct {
	Name        Permission `json:"name"`
	Description string     `json:"description"`
}

// Permissions lists every permission in the order the admin console shows them.
var Permissions = []Info{
	{RepoRead, "See the role's repositories: their wiki, files and search"},
	{RepoChat, "Ask questions about the role's repositories"},
	{RepoSync, "Sync the role's repositories with their git server"},
	{RepoImport, "Import repositories from the git servers; a new one is added to the importer's roles that hold this permission"},
	{JobsView, "See the indexing and wiki jobs of the repositories they can read, with live output"},
	{UsersManage, "Manage users and roles, which includes granting any permission"},
	{ServersManage, "Manage git servers and their access tokens"},
}

func known(p Permission) bool {
	for _, i := range Permissions {
		if i.Name == p {
			return true
		}
	}
	return false
}
