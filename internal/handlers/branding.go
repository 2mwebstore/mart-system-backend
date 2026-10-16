package handlers

// Company branding: the system's name (shown in the sidebar, sign-in pages and
// the browser tab instead of "Com Mart Control System") and the company logo.
// Both are settings; the logo is an uploaded image (a data URL) or a web link.
// Read publicly — the sign-in pages, the customer display and the QR table
// menu show them before anyone signs in. The logo is served as an image of
// its own (cacheable) so it isn't repeated inside every JSON response.

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"com-mart/backend/internal/models"
	"com-mart/backend/internal/utils"
)

const (
	settingCompanyName = "company_name"
	settingCompanyLogo = "company_logo"
	maxLogoLen         = 2_000_000 // ~1.4 MB image as a data URL; the browser shrinks logos first
	defaultStoreName   = "Com Mart"
)

// Raster images only: an SVG can carry script, and the logo is served from
// the API's own origin.
var logoTypes = []string{"data:image/png;base64,", "data:image/jpeg;base64,", "data:image/webp;base64,", "data:image/gif;base64,"}

type brandingDTO struct {
	CompanyName string `json:"company_name"` // "" = use the app's default name
	HasLogo     bool   `json:"has_logo"`
	LogoLink    string `json:"logo_link"`    // set when the logo is a web link
	LogoVersion string `json:"logo_version"` // changes whenever an uploaded logo changes (cache key)
}

func (a *API) brandingSettings() (name, logo string) {
	var rows []models.Setting
	a.DB.Where("`key` IN ?", []string{settingCompanyName, settingCompanyLogo}).Find(&rows)
	for _, r := range rows {
		switch r.Key {
		case settingCompanyName:
			name = r.Value
		case settingCompanyLogo:
			logo = r.Value
		}
	}
	return name, logo
}

func toBrandingDTO(name, logo string) brandingDTO {
	out := brandingDTO{CompanyName: name, HasLogo: logo != ""}
	switch {
	case strings.HasPrefix(logo, "data:image/"):
		sum := sha256.Sum256([]byte(logo))
		out.LogoVersion = hex.EncodeToString(sum[:6])
	case logo != "":
		out.LogoLink = logo
	}
	return out
}

// GetBranding is public: company name and how to show the logo.
func (a *API) GetBranding(c *gin.Context) {
	name, logo := a.brandingSettings()
	utils.OK(c, http.StatusOK, toBrandingDTO(name, logo))
}

// GetBrandingLogo serves an uploaded logo as an image. With ?v=<version> the
// response can be cached for good (a new logo gets a new version).
func (a *API) GetBrandingLogo(c *gin.Context) {
	_, logo := a.brandingSettings()
	if !strings.HasPrefix(logo, "data:image/") {
		if logo != "" {
			c.Redirect(http.StatusFound, logo)
			return
		}
		c.Status(http.StatusNotFound)
		return
	}
	header, data, ok := strings.Cut(logo, ",")
	if !ok || !strings.HasSuffix(header, ";base64") {
		c.Status(http.StatusNotFound)
		return
	}
	raw, err := base64.StdEncoding.DecodeString(data)
	if err != nil {
		c.Status(http.StatusNotFound)
		return
	}
	version := toBrandingDTO("", logo).LogoVersion
	c.Header("ETag", `"`+version+`"`)
	c.Header("X-Content-Type-Options", "nosniff")
	// An <img> fetch carries no Origin, so it gets no CORS headers; without
	// this, a later CORS fetch of the same URL would reuse that cached copy.
	c.Header("Vary", "Origin")
	if c.Query("v") == version {
		c.Header("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		c.Header("Cache-Control", "no-cache")
	}
	if c.GetHeader("If-None-Match") == `"`+version+`"` {
		c.Status(http.StatusNotModified)
		return
	}
	c.Data(http.StatusOK, strings.TrimSuffix(strings.TrimPrefix(header, "data:"), ";base64"), raw)
}

type brandingReq struct {
	CompanyName string `json:"company_name" binding:"max=80"`
	// The logo: an image data URL (PNG / JPEG / WebP / GIF), an https link,
	// "" to remove it, or left out to keep the current one (so saving just the
	// name doesn't re-upload the image).
	Logo *string `json:"logo"`
}

func validLogo(s string) bool {
	if s == "" {
		return true
	}
	if strings.HasPrefix(s, "https://") || strings.HasPrefix(s, "http://") {
		return len(s) <= 2000
	}
	if len(s) > maxLogoLen {
		return false
	}
	for _, p := range logoTypes {
		if strings.HasPrefix(s, p) {
			_, err := base64.StdEncoding.DecodeString(s[len(p):])
			return err == nil
		}
	}
	return false
}

func (a *API) UpdateBranding(c *gin.Context) {
	var req brandingReq
	if !bind(c, &req) {
		return
	}
	name := strings.TrimSpace(req.CompanyName)
	beforeName, beforeLogo := a.brandingSettings()
	logo := beforeLogo
	if req.Logo != nil {
		logo = strings.TrimSpace(*req.Logo)
	}
	if !validLogo(logo) {
		invalid(c, "logo", "The logo must be a PNG, JPEG, WebP or GIF image (up to about 1.4 MB), or a web link.")
		return
	}
	pairs := map[string]string{settingCompanyName: name}
	if req.Logo != nil {
		pairs[settingCompanyLogo] = logo
	}
	// Receipts, invoices and the QR menu print the receipt header as the
	// store's name. While that's still the shipped "Com Mart" (or blank, or
	// the old company name) it follows the company name; a header someone
	// wrote on purpose is left alone.
	var header models.Setting
	a.DB.Where("`key` = ?", settingReceiptHeader).Limit(1).Find(&header)
	headerFollows := name != beforeName && (header.Value == "" || header.Value == defaultStoreName || header.Value == beforeName)
	if headerFollows {
		pairs[settingReceiptHeader] = name // "" falls back to the default again (loadSettings)
	}
	err := a.DB.Transaction(func(tx *gorm.DB) error {
		for k, v := range pairs {
			row := models.Setting{Key: k, Value: v}
			if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "key"}}, DoUpdates: clause.AssignmentColumns([]string{"value", "updated_at"})}).Create(&row).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		dbFail(c, err)
		return
	}
	// The audit row records whether a logo was set, not the image itself.
	before := gin.H{"company_name": beforeName, "has_logo": beforeLogo != ""}
	after := gin.H{"company_name": name, "has_logo": logo != ""}
	if headerFollows {
		before["receipt_header"], after["receipt_header"] = header.Value, name
	}
	a.audit(c, "settings", "settings.branding", "settings", 0, before, after)
	utils.OK(c, http.StatusOK, toBrandingDTO(name, logo))
}
