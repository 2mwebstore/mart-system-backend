package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"com-mart/backend/internal/dto"
	"com-mart/backend/internal/middleware"
	"com-mart/backend/internal/services"
	"com-mart/backend/internal/utils"
)

type AuthHandler struct {
	authService *services.AuthService
}

func NewAuthHandler(authService *services.AuthService) *AuthHandler {
	return &AuthHandler{authService: authService}
}

func (h *AuthHandler) Login(c *gin.Context) {
	var req dto.LoginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		utils.RespondError(c, utils.NewValidationError(map[string]string{"_": err.Error()}))
		return
	}

	resp, err := h.authService.Login(req.Username, req.Password, c.ClientIP())
	if err != nil {
		utils.RespondError(c, err)
		return
	}
	utils.OK(c, http.StatusOK, resp)
}

func (h *AuthHandler) PINLogin(c *gin.Context) {
	var req dto.PINLoginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		utils.RespondError(c, utils.NewValidationError(map[string]string{"_": err.Error()}))
		return
	}

	resp, err := h.authService.PINLogin(req.DeviceKey, req.Username, req.PIN, c.ClientIP())
	if err != nil {
		utils.RespondError(c, err)
		return
	}
	utils.OK(c, http.StatusOK, resp)
}

func (h *AuthHandler) Refresh(c *gin.Context) {
	var req dto.RefreshRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		utils.RespondError(c, utils.NewValidationError(map[string]string{"_": err.Error()}))
		return
	}

	resp, err := h.authService.Refresh(req.RefreshToken)
	if err != nil {
		utils.RespondError(c, err)
		return
	}
	utils.OK(c, http.StatusOK, resp)
}

func (h *AuthHandler) Logout(c *gin.Context) {
	var req dto.LogoutRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		utils.RespondError(c, utils.NewValidationError(map[string]string{"_": err.Error()}))
		return
	}

	if err := h.authService.Logout(req.RefreshToken); err != nil {
		utils.RespondError(c, err)
		return
	}
	utils.OK(c, http.StatusOK, gin.H{"logged_out": true})
}

func (h *AuthHandler) Me(c *gin.Context) {
	resp, err := h.authService.Me(middleware.UserIDFrom(c))
	if err != nil {
		utils.RespondError(c, err)
		return
	}
	utils.OK(c, http.StatusOK, resp)
}
