// Package usageapi serves the admin console's usage: the tokens the model
// requests of each repository spent, per model.
package usageapi

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"deepcode/internal/access/role"
	"deepcode/internal/app/server/httpx"
	"deepcode/internal/platform/ai/usage"
)

// maxDays bounds ?days=, about ten years.
const maxDays = 3660

// Mount adds GET /usage?days= to r: each repository's spend on each model
// over the last days days (30 when unset; 0 for all of them), behind jobs.view
// and of the repositories the viewer may read, as the jobs that spent it are.
// A viewer who may read every repository sees too what no repository asked
// for and what deleted ones spent.
func Mount(r gin.IRouter, m *usage.Manager) {
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
