package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"com-mart/backend/internal/utils"
)

func Health(c *gin.Context) {
	utils.OK(c, http.StatusOK, gin.H{"status": "ok"})
}
