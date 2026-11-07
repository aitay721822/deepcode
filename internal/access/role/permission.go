// Package role keeps the roles of role-based access control: each grants a
// set of permissions, either on every repository or on the repositories
// listed for it.
package role

import "deepcode/internal/access/role/store"

// Permission names one thing a user may do. The repo.read, repo.chat and
// repo.sync permissions (repoScoped) apply to a role's repositories. The
// others are not tied to a role's repositories, though jobs.view shows only
// the jobs of repositories the user can read.
type Permission = store.Permission

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
	{RepoRead, "檢視此角色的 Repository：Wiki、檔案與搜尋"},
	{RepoChat, "針對此角色的 Repository 提問"},
	{RepoSync, "將此角色的 Repository 與其 Git 伺服器同步"},
	{RepoImport, "從 Git 伺服器匯入 Repository；新匯入的 Repository 會加入匯入者擁有此權限的角色"},
	{JobsView, "檢視自己可讀取的 Repository 的索引與 Wiki 工作，以及即時輸出"},
	{UsersManage, "管理使用者與角色，包括授予任何權限"},
	{ServersManage, "管理 Git 伺服器及其存取 Token"},
}

func known(p Permission) bool {
	for _, i := range Permissions {
		if i.Name == p {
			return true
		}
	}
	return false
}
