package server

import (
	"net/http"
	"os"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"

	"github.com/gin-gonic/gin"
)

// ProvideUpdateHTTPServer creates the public, unauthenticated update listener.
// It is intentionally separate from the existing business/admin listener so
// nginx can expose only the update surface on updata.yingzo.art.
type UpdateHTTPServer struct {
	Server *http.Server
}

func ProvideUpdateHTTPServer(h *handler.Handlers) *UpdateHTTPServer {
	r := gin.New()
	r.Use(middleware.Recovery())
	r.Use(func(c *gin.Context) {
		c.Header("Access-Control-Allow-Origin", "*")
		c.Header("Access-Control-Allow-Methods", "GET, HEAD, OPTIONS")
		c.Header("Access-Control-Allow-Headers", "Content-Type")
		if c.Request.Method == http.MethodOptions {
			c.Status(http.StatusNoContent)
			c.Abort()
			return
		}
		c.Next()
	})
	r.GET("/health", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"status": "ok"}) })
	updates := r.Group("/v1/updates")
	updates.GET("/check", h.Admin.DesktopUpdate.PublicCheck)
	updates.GET("/latest.yml", h.Admin.DesktopUpdate.PublicMetadata)
	updates.HEAD("/latest.yml", h.Admin.DesktopUpdate.PublicMetadata)
	updates.GET("/latest-mac.yml", h.Admin.DesktopUpdate.PublicMetadata)
	updates.HEAD("/latest-mac.yml", h.Admin.DesktopUpdate.PublicMetadata)
	updates.GET("/metadata/:id", h.Admin.DesktopUpdate.PublicMetadata)
	updates.HEAD("/metadata/:id", h.Admin.DesktopUpdate.PublicMetadata)
	updates.GET("/download/:id", h.Admin.DesktopUpdate.PublicDownload)
	updates.HEAD("/download/:id", h.Admin.DesktopUpdate.PublicDownload)

	addr := os.Getenv("YINGZO_UPDATE_ADDR")
	if addr == "" {
		addr = "0.0.0.0:8081"
	}
	return &UpdateHTTPServer{Server: &http.Server{
		Addr:              addr,
		Handler:           r,
		ReadHeaderTimeout: 30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}}
}
