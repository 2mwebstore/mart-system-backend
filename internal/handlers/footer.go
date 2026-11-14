package handlers

// The footer: one line under every back-office page and on the sign-in page,
// the same for every company (a copyright, say). The super admin writes it on
// Platform > System. "{year}" in it shows as the current year, worked out by
// the browser. Until the super admin saves one it's defaultFooterText; saved
// empty, there's no footer. It's read publicly with the branding (GET
// /auth/branding), which every page loads before anyone signs in.

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm/clause"

	"com-mart/backend/internal/models"
	"com-mart/backend/internal/utils"
)

const (
	settingFooterText = "footer_text"
	defaultFooterText = "© {year} Com Mart. All rights reserved."
)

// footerText is the footer as saved, or the default when it never was.
func (a *API) footerText() string {
	var row models.Setting
	res := a.DB.Where("`key` = ?", settingFooterText).Limit(1).Find(&row)
	if res.Error != nil || res.RowsAffected == 0 {
		return defaultFooterText
	}
	return row.Value
}

type footerDTO struct {
	Text string `json:"text"`
}

type footerReq struct {
	Text string `json:"text" binding:"max=200"`
}

func (a *API) GetFooter(c *gin.Context) {
	utils.OK(c, http.StatusOK, footerDTO{Text: a.footerText()})
}

// UpdateFooter saves the footer as one line: line breaks and runs of spaces
// become single spaces.
func (a *API) UpdateFooter(c *gin.Context) {
	var req footerReq
	if !bind(c, &req) {
		return
	}
	text := strings.Join(strings.Fields(req.Text), " ")
	before := a.footerText()
	row := models.Setting{Key: settingFooterText, Value: text}
	err := a.DB.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "key"}}, DoUpdates: clause.AssignmentColumns([]string{"value", "updated_at"})}).Create(&row).Error
	if err != nil {
		dbFail(c, err)
		return
	}
	a.audit(c, "settings", "footer.update", "settings", 0, footerDTO{Text: before}, footerDTO{Text: text})
	utils.OK(c, http.StatusOK, footerDTO{Text: text})
}
