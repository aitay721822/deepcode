package repoapi

import (
	"archive/zip"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"deepcode/internal/access/role"
	"deepcode/internal/access/session"
	"deepcode/internal/app/server/httpx"
	"deepcode/internal/knowledge/chat"
	wikiview "deepcode/internal/knowledge/wiki/view"
)

func (a *API) wiki(c *gin.Context) {
	wk, err := a.Wiki.Load(c.Request.Context(), httpx.RepoID(c))
	if err != nil {
		httpx.InternalError(c, err)
		return
	}
	c.JSON(http.StatusOK, wk)
}

// exportWiki streams the shown wiki as a zip of Markdown pages and a README
// index, linked at its commit; a repository with no pages has none to export.
func (a *API) exportWiki(c *gin.Context) {
	ctx := c.Request.Context()
	repo, err := a.Catalog.Get(ctx, httpx.RepoID(c))
	if err != nil {
		httpx.DomainError(c, err)
		return
	}
	wk, err := a.Wiki.Load(ctx, repo.ID)
	if err != nil {
		httpx.InternalError(c, err)
		return
	}
	if len(wk.Pages) == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "這個儲存庫還沒有 wiki"})
		return
	}
	name := fmt.Sprintf("%s-%s-wiki-%s.zip", filename(repo.Owner), filename(repo.Name), short(wk.CommitSHA))
	c.Header("Content-Type", "application/zip")
	c.Header("Content-Disposition", `attachment; filename="`+name+`"`)
	zw := zip.NewWriter(c.Writer)
	for _, f := range wikiview.Export(wk, repo.Provider, repo.URL) {
		w, err := zw.Create(f.Name)
		if err != nil {
			log.Printf("wiki export %s: %v", f.Name, err)
			break
		}
		if _, err := io.WriteString(w, f.Body); err != nil {
			log.Printf("wiki export %s: %v", f.Name, err)
			break
		}
	}
	if err := zw.Close(); err != nil {
		log.Printf("wiki export: %v", err)
	}
}

// filename keeps the characters a download's name may hold, others becoming -.
func filename(s string) string {
	return strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.' {
			return r
		}
		return '-'
	}, s)
}

// short is the first seven characters of a commit.
func short(sha string) string { return sha[:min(7, len(sha))] }

// chatStream answers a question about a repository the user may ask about,
// as server-sent events. The body names the repository by its public id.
func (a *API) chatStream(c *gin.Context) {
	var body struct {
		chat.Request
		Repo string `json:"repoId"`
	}
	if !httpx.Bind(c, &body) {
		return
	}
	req := body.Request
	var err error
	if req.RepoID, err = a.Catalog.ID(c.Request.Context(), body.Repo); err != nil {
		httpx.DomainError(c, err)
		return
	}
	if !httpx.Visible(c, role.RepoChat, req.RepoID) {
		return
	}
	req.UserID = httpx.Principal(c).UserID
	stream := httpx.OpenStream(c)
	ctx, stop := stream.Watch(c, a.Sessions, func(p *session.Principal) bool {
		return p.CanOn(role.RepoRead, req.RepoID) && p.CanOn(role.RepoChat, req.RepoID)
	})
	defer stop()
	emit := func(event string, data any) { stream.Send(event, 0, data) }
	if err := a.Chat.Ask(ctx, req, emit); err != nil && ctx.Err() == nil {
		msg, logIt := chat.Explain(err)
		if logIt {
			log.Printf("chat: repository %d: %v", req.RepoID, err)
		}
		emit("error", gin.H{"message": msg})
	}
}

// conversation returns one of the user's conversations, while they may still
// read its repository.
func (a *API) conversation(c *gin.Context) {
	id, ok := httpx.PathID(c)
	if !ok {
		return
	}
	conv, err := a.Chat.Get(c.Request.Context(), id, httpx.Principal(c).UserID)
	if err != nil {
		httpx.DomainError(c, err)
		return
	}
	if httpx.Readable(c, conv.RepoID, chat.ErrNotFound) {
		c.JSON(http.StatusOK, conv)
	}
}
