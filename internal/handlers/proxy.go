package handlers

import (
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"jetstream/internal/config"

	"github.com/gin-gonic/gin"
)

type ProxyHandler struct {
	target *url.URL
	proxy  *httputil.ReverseProxy
}

func NewProxyHandler(cfg *config.Config) *ProxyHandler {
	target, err := url.Parse(cfg.NavidromeURL)
	if err != nil {
		log.Fatalf("Invalid Navidrome URL: %v", err)
	}

	proxy := httputil.NewSingleHostReverseProxy(target)
	// Flush immediately to support SSE (Server Sent Events)
	proxy.FlushInterval = -1

	// Optional: Custom error handling or request logic for proxy
	originalDirector := proxy.Director
	proxy.Director = func(req *http.Request) {
		originalDirector(req)
		// Ensure Host header matches target for some servers (though Navidrome usually doesn't care)
		req.Host = target.Host
	}

	return &ProxyHandler{
		target: target,
		proxy:  proxy,
	}
}

func (h *ProxyHandler) GetTargetURL() string {
	return h.target.String()
}

func (h *ProxyHandler) Handle(c *gin.Context) {
	h.proxy.ServeHTTP(c.Writer, c.Request)
}
