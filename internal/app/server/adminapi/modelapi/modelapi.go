// Package modelapi serves the admin console's models: the chat model as it
// stands (model) and the tokens each repository's requests spent, per model.
package modelapi

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"deepcode/internal/access/role"
	"deepcode/internal/app/server/httpx"
	"deepcode/internal/platform/ai"
	"deepcode/internal/platform/ai/usage"
)

// maxDays bounds ?days=, about ten years.
const maxDays = 3660

// Mount adds to r, behind jobs.view, GET /model, chat as it stands (model;
// chat is nil when none is set), and GET /usage?days=, each repository's spend
// on each model over the last days days (30 when unset; 0 for all of them), of
// the repositories the viewer may read, as the jobs that spent it are. A
// viewer who may read every repository sees too what no repository asked for
// and what deleted ones spent.
func Mount(r gin.IRouter, m *usage.Manager, chat ai.Chat) {
	r.GET("/model", httpx.Require(role.JobsView), func(c *gin.Context) { c.JSON(http.StatusOK, describe(chat)) })
	r.GET("/usage", httpx.Require(role.JobsView), func(c *gin.Context) {
		days, err := strconv.Atoi(c.DefaultQuery("days", "30"))
		if err != nil || days < 0 || days > maxDays {
			httpx.Fail(c, http.StatusBadRequest, "days 必須介於 0 到 "+strconv.Itoa(maxDays)+"，0 代表全部")
			return
		}
		totals, err := m.Totals(c.Request.Context(), days, func(repoID int64) bool { return httpx.CanRead(c, repoID) })
		if err != nil {
			httpx.InternalError(c, err)
			return
		}
		c.JSON(http.StatusOK, totals)
	})
}
