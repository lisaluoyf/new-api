package controller

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func DownloadTopupInvoice(c *gin.Context)      { downloadTopupInvoice(c, false) }
func AdminDownloadTopupInvoice(c *gin.Context) { downloadTopupInvoice(c, true) }

func downloadTopupInvoice(c *gin.Context, admin bool) {
	c.Header("Cache-Control", "private, no-store")
	if c.GetInt("id") <= 0 {
		c.AbortWithStatus(http.StatusUnauthorized)
		return
	}
	if admin && c.GetInt("role") < common.RoleAdminUser {
		c.AbortWithStatus(http.StatusForbidden)
		return
	}
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		c.AbortWithStatus(http.StatusBadRequest)
		return
	}
	invoice, err := model.GetTopupInvoice(id, c.GetInt("id"), admin)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		c.AbortWithStatus(http.StatusNotFound)
		return
	}
	if errors.Is(err, model.ErrInvoiceUnavailable) {
		c.AbortWithStatus(http.StatusConflict)
		return
	}
	if err != nil {
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	content, err := service.RenderTopupInvoice(invoice)
	if err != nil {
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	c.Header("Content-Disposition", fmt.Sprintf(`attachment; filename="invoice-APIM-%d.pdf"`, invoice.ID))
	c.Header("X-Content-Type-Options", "nosniff")
	c.Data(http.StatusOK, "application/pdf", content)
}
