package repoapi

import (
	"log"
	"net/http"

	"github.com/gin-gonic/gin"

	"deepcode/internal/access/role"
	"deepcode/internal/access/session"
	"deepcode/internal/app/server/httpx"
	"deepcode/internal/knowledge/chat"
)

func (a *API) wiki(c *gin.Context) {
	wk, err := a.Wiki.Load(c.Request.Context(), httpx.RepoID(c))
	if err != nil {
		httpx.InternalError(c, err)
		return
	}
	c.JSON(http.StatusOK, wk)
}

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
