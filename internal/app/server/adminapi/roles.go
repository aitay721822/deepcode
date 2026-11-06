package adminapi

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"deepcode/internal/access/role"
	"deepcode/internal/app/server/httpx"
)

func (a *API) listRoles(c *gin.Context) {
	list, err := a.Roles.List(c.Request.Context())
	reply(c, http.StatusOK, list, err)
}

func (a *API) createRole(c *gin.Context) {
	var in role.Input
	if !httpx.Bind(c, &in) {
		return
	}
	r, err := a.Roles.Create(c.Request.Context(), in)
	reply(c, http.StatusCreated, r, err)
}

func (a *API) updateRole(c *gin.Context) {
	id, ok := httpx.PathID(c)
	var in role.Input
	if !ok || !httpx.Bind(c, &in) {
		return
	}
	r, err := a.Roles.Update(c.Request.Context(), id, in)
	reply(c, http.StatusOK, r, err)
}

func (a *API) deleteRole(c *gin.Context) {
	if id, ok := httpx.PathID(c); ok {
		reply(c, http.StatusNoContent, nil, a.Roles.Delete(c.Request.Context(), id))
	}
}
