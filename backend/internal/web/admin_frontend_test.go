//go:build embed

package web

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

// Use both production entrypoints: a legacy-only local build would hide a
// regression where admin history routes receive the public SPA's HTML.
func TestAdminFrontendRouting(t *testing.T) {
	const publicHTML = "<html><head></head><body>public portal</body></html>"
	const adminHTML = "<html><body>legacy admin</body></html>"
	const asset = "console.log('admin asset')"
	distFS := fstest.MapFS{
		"index.html":                   {Data: []byte(publicHTML)},
		"admin/index.html":             {Data: []byte(adminHTML)},
		"admin/assets/app-12345678.js": {Data: []byte(asset)},
	}

	for _, mode := range []string{"normal", "fallback", "setup"} {
		t.Run(mode, func(t *testing.T) {
			var handler gin.HandlerFunc
			if mode == "normal" {
				server := &FrontendServer{
					distFS:     distFS,
					fileServer: http.FileServer(http.FS(distFS)),
					baseHTML:   []byte(publicHTML),
					cache:      NewHTMLCache(),
					settings:   &mockSettingsProvider{settings: map[string]string{}},
				}
				handler = server.Middleware()
			} else {
				handler = serveEmbeddedFrontend(distFS, mode == "setup")
			}
			router := gin.New()
			router.Use(handler)
			router.GET("/setup/status", func(c *gin.Context) {
				c.JSON(http.StatusOK, gin.H{"needs_setup": mode == "setup"})
			})
			router.POST("/setup/test-db", func(c *gin.Context) {
				c.Status(http.StatusNoContent)
			})

			for _, path := range []string{
				"/admin", "/admin/", "/admin/index.html", "/admin/login",
				"/admin/setup", "/admin/admin/dashboard", "/admin/admin/settings?tab=general",
			} {
				t.Run(path, func(t *testing.T) {
					w := httptest.NewRecorder()
					router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
					if mode == "setup" && (path == "/admin" || path == "/admin/" || path == "/admin/index.html") {
						assert.Equal(t, http.StatusTemporaryRedirect, w.Code)
						assert.Equal(t, "/admin/setup", w.Header().Get("Location"))
						return
					}
					assert.Equal(t, http.StatusOK, w.Code)
					assert.Contains(t, w.Header().Get("Content-Type"), "text/html")
					assert.Equal(t, adminHTML, w.Body.String())
					assert.Equal(t, "no-cache", w.Header().Get("Cache-Control"))
				})
			}

			for _, path := range []string{"/", "/index.html", "/setup?source=install"} {
				w := httptest.NewRecorder()
				router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
				if mode == "setup" || path == "/setup?source=install" {
					assert.Equal(t, http.StatusTemporaryRedirect, w.Code, path)
					target := "/admin/setup"
					if path == "/setup?source=install" {
						target += "?source=install"
					}
					assert.Equal(t, target, w.Header().Get("Location"), path)
				} else {
					// FileServer may canonicalize /index.html to / in fallback mode.
					if path == "/index.html" && mode == "fallback" {
						assert.Equal(t, http.StatusMovedPermanently, w.Code)
						continue
					}
					assert.Equal(t, http.StatusOK, w.Code, path)
					assert.Contains(t, w.Body.String(), "public portal", path)
					assert.NotContains(t, w.Body.String(), "legacy admin", path)
				}
			}

			w := httptest.NewRecorder()
			router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/admin/assets/app-12345678.js", nil))
			assert.Equal(t, http.StatusOK, w.Code)
			assert.Equal(t, asset, w.Body.String())
			assert.Contains(t, w.Header().Get("Content-Type"), "javascript")

			w = httptest.NewRecorder()
			router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/setup/status", nil))
			assert.Equal(t, http.StatusOK, w.Code)
			assert.Contains(t, w.Header().Get("Content-Type"), "application/json")

			w = httptest.NewRecorder()
			router.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/setup/test-db", nil))
			assert.Equal(t, http.StatusNoContent, w.Code)
		})
	}
}

func TestSetupFrontendWithoutAdminBundle(t *testing.T) {
	const legacyHTML = "<html>standalone legacy frontend</html>"
	distFS := fstest.MapFS{"index.html": {Data: []byte(legacyHTML)}}
	router := gin.New()
	router.Use(serveEmbeddedFrontend(distFS, true))

	for _, path := range []string{"/", "/admin", "/admin/", "/admin/index.html"} {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		assert.Equal(t, http.StatusTemporaryRedirect, w.Code, path)
		assert.Equal(t, "/setup", w.Header().Get("Location"), path)
	}
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/setup", nil))
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, legacyHTML, w.Body.String())
}
