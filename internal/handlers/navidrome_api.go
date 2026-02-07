package handlers

import (
	"jetstream/internal/service"

	"github.com/gin-gonic/gin"
)

type NavidromeAPIHandler struct {
	squidService *service.SquidService
	proxyHandler *ProxyHandler
}

func NewNavidromeAPIHandler(squidService *service.SquidService, proxyHandler *ProxyHandler) *NavidromeAPIHandler {
	return &NavidromeAPIHandler{
		squidService: squidService,
		proxyHandler: proxyHandler,
	}
}

func (h *NavidromeAPIHandler) SearchSongs(c *gin.Context) {
	// For now, just proxy to see the body in the debug logs
	h.proxyHandler.Handle(c)
}

func (h *NavidromeAPIHandler) SearchAlbums(c *gin.Context) {
	h.proxyHandler.Handle(c)
}

func (h *NavidromeAPIHandler) SearchArtists(c *gin.Context) {
	h.proxyHandler.Handle(c)
}
