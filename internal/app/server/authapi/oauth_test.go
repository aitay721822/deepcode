package authapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"deepcode/internal/access/oauth"
)

// TestOAuthError: an unknown app is told 401, any other OAuth failure 400.
func TestOAuthError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for code, status := range map[string]int{"invalid_client": http.StatusUnauthorized, "invalid_grant": http.StatusBadRequest} {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		oauthError(c, &oauth.Error{Code: code})
		var body struct{ Error string }
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || w.Code != status || body.Error != code {
			t.Errorf("%s: %d %s", code, w.Code, w.Body)
		}
	}
}
