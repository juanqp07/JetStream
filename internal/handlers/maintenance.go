package handlers

import (
	"jetstream/internal/service"
	"net/http"

	"github.com/gin-gonic/gin"
)

type MaintenanceHandler struct {
	syncService *service.SyncService
}

func NewMaintenanceHandler(syncService *service.SyncService) *MaintenanceHandler {
	return &MaintenanceHandler{
		syncService: syncService,
	}
}

func (h *MaintenanceHandler) Scan(c *gin.Context) {
	total, corrupt, err := h.syncService.MaintenanceScan(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"status":          "completed",
		"total_files":     total,
		"corrupt_deleted": corrupt,
	})
}
