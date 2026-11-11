package hooks

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"deepcode/internal/access/role"
	"deepcode/internal/app/server/httpx"
	"deepcode/internal/repo/catalog"
	"deepcode/internal/repo/gitserver"
)

// API receives push webhooks and manages the secret that signs them.
type API struct {
	Catalog *catalog.Catalog
	Box     *gitserver.Box
}

// MountPublic adds the hook route, which a git server calls without a
// session: its signature is the authentication.
func (a *API) MountPublic(r gin.IRouter) {
	r.POST("/hooks/:id", a.receive)
}

// MountAdmin adds the secret's management routes to a signed-in router.
func (a *API) MountAdmin(r gin.IRouter) {
	sync := httpx.OnRepo(a.Catalog.ID, role.RepoSync)
	r.GET("/repos/:id/webhook", sync, a.show)
	r.POST("/repos/:id/webhook", sync, a.set)
	r.DELETE("/repos/:id/webhook", sync, a.clear)
}

// show reports whether a secret is set; the secret itself is never returned.
func (a *API) show(c *gin.Context) {
	sealed, err := a.Catalog.Store.WebhookSecret(c.Request.Context(), httpx.RepoID(c))
	if err != nil {
		httpx.InternalError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"hasSecret": sealed != ""})
}

// set stores a provided secret, or a generated one, and returns it once.
func (a *API) set(c *gin.Context) {
	var body struct {
		Secret string `json:"secret"`
	}
	if !httpx.Bind(c, &body) {
		return
	}
	secret := strings.TrimSpace(body.Secret)
	if secret == "" {
		var err error
		if secret, err = NewSecret(); err != nil {
			httpx.InternalError(c, err)
			return
		}
	} else if len(secret) < 32 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "密鑰至少要 32 個字元"})
		return
	}
	if err := a.Catalog.Store.SetWebhookSecret(c.Request.Context(), httpx.RepoID(c), a.Box.Seal(secret)); err != nil {
		httpx.InternalError(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"secret": secret})
}

// clear turns the hook off.
func (a *API) clear(c *gin.Context) {
	if err := a.Catalog.Store.SetWebhookSecret(c.Request.Context(), httpx.RepoID(c), ""); err != nil {
		httpx.InternalError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// receive verifies a push and syncs the repository. A ping, another branch
// or a branch deletion is answered 200 and ignored.
func (a *API) receive(c *gin.Context) {
	ctx := c.Request.Context()
	id, err := a.Catalog.ID(ctx, c.Param("id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "找不到 Repository"})
		return
	}
	sealed, err := a.Catalog.Store.WebhookSecret(ctx, id)
	if err != nil {
		httpx.InternalError(c, err)
		return
	}
	secret, err := a.Box.Open(sealed)
	if err != nil {
		log.Printf("webhook %s: %v", c.Param("id"), err) // never the secret
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Webhook 密鑰無法解開，請重新產生"})
		return
	}
	if secret == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "沒有可用的 Webhook 密鑰"})
		return
	}
	github, gitlab := c.GetHeader("X-Hub-Signature-256"), c.GetHeader("X-Gitlab-Token")
	if github == "" && gitlab == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "缺少簽章"})
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(c.Writer, c.Request.Body, 10<<20))
	if err != nil {
		c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "Webhook 內容太大"})
		return
	}
	switch {
	case github != "":
		if !VerifyGitHub(secret, body, github) {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "簽章不正確"})
			return
		}
	case gitlab != "":
		if !VerifyGitLab(secret, gitlab) {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Token 不正確"})
			return
		}
	}
	if c.GetHeader("X-GitHub-Event") == "ping" || c.GetHeader("X-Gitlab-Event") == "System Hook" {
		c.JSON(http.StatusOK, gin.H{"ok": true})
		return
	}
	var push struct {
		Ref         string `json:"ref"`
		After       string `json:"after"`
		CheckoutSHA string `json:"checkout_sha"`
	}
	if err := json.Unmarshal(body, &push); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Webhook 內容不是 JSON"})
		return
	}
	branch, err := a.Catalog.Branch(ctx, id)
	if err != nil {
		httpx.InternalError(c, err)
		return
	}
	if push.Ref != "refs/heads/"+branch || push.After == "" || strings.Trim(push.After, "0") == "" {
		c.JSON(http.StatusOK, gin.H{"ignored": true})
		return
	}
	job, err := a.Catalog.Sync(ctx, id)
	switch {
	case err != nil:
		httpx.DomainError(c, err)
	case job == nil:
		c.JSON(http.StatusOK, gin.H{"upToDate": true})
	default:
		c.JSON(http.StatusAccepted, gin.H{"accepted": true})
	}
}
