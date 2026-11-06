package repoapi

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"deepcode/internal/app/server/httpx"
	"deepcode/internal/knowledge/retrieval"
	"deepcode/internal/repo/catalog"
)

// snapshot resolves ?snapshot= (which must belong to the repository) or the
// published snapshot.
func (a *API) snapshot(c *gin.Context) (catalog.Ref, bool) {
	repoID, ok := httpx.PathID(c)
	if !ok {
		return catalog.Ref{}, false
	}
	var id int64
	if v := c.Query("snapshot"); v != "" {
		var err error
		if id, err = strconv.ParseInt(v, 10, 64); err != nil {
			httpx.Fail(c, http.StatusBadRequest, "invalid snapshot")
			return catalog.Ref{}, false
		}
	}
	ref, err := a.Catalog.Resolve(c.Request.Context(), repoID, id)
	if err != nil {
		httpx.DomainError(c, err)
		return catalog.Ref{}, false
	}
	return ref, true
}

// listFiles returns the snapshot's file list.
func (a *API) listFiles(c *gin.Context) {
	snap, ok := a.snapshot(c)
	if !ok {
		return
	}
	b, err := a.Catalog.FilesJSON(c.Request.Context(), snap.ID)
	if err != nil {
		httpx.InternalError(c, err)
		return
	}
	c.Data(http.StatusOK, "application/json", b)
}

func (a *API) getFile(c *gin.Context) {
	snap, ok := a.snapshot(c)
	if !ok {
		return
	}
	f, err := a.Catalog.File(c.Request.Context(), snap, strings.TrimPrefix(c.Param("path"), "/"))
	if err != nil {
		httpx.DomainError(c, err)
		return
	}
	c.JSON(http.StatusOK, f)
}

func (a *API) symbols(c *gin.Context) {
	snap, ok := a.snapshot(c)
	if !ok {
		return
	}
	q, ok := query(c)
	if !ok {
		return
	}
	hits, err := a.Retriever.Symbols(c.Request.Context(), snap.ID, q, 50)
	if err != nil {
		httpx.InternalError(c, err)
		return
	}
	c.JSON(http.StatusOK, hits)
}

// search runs the hybrid retrieval; chunk contents are trimmed for display.
func (a *API) search(c *gin.Context) {
	snap, ok := a.snapshot(c)
	if !ok {
		return
	}
	q, ok := query(c)
	if !ok {
		return
	}
	if q == "" {
		httpx.Fail(c, http.StatusBadRequest, "q is required")
		return
	}
	res, err := a.Retriever.Search(c.Request.Context(), snap.ID, q, 20)
	if err != nil {
		httpx.InternalError(c, err)
		return
	}
	for i := range res.Hits {
		if content := res.Hits[i].Content; len(content) > 1500 {
			res.Hits[i].Content = strings.ToValidUTF8(content[:1500], "") + "\n…"
		}
	}
	if res.Hits == nil {
		res.Hits = []retrieval.Hit{}
	}
	c.JSON(http.StatusOK, res)
}

// maxQuery bounds ?q=: it reaches the embedding API and becomes SQL parameters.
const maxQuery = 1000

func query(c *gin.Context) (string, bool) {
	q := strings.TrimSpace(c.Query("q"))
	if len(q) > maxQuery {
		httpx.Fail(c, http.StatusBadRequest, fmt.Sprintf("q is longer than %d bytes", maxQuery))
		return "", false
	}
	return q, true
}
