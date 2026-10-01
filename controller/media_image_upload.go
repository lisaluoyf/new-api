package controller

import (
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

func UploadMediaImage(c *gin.Context) {
	const maxBytes = 20 << 20
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBytes+(1<<20))
	if err := c.Request.ParseMultipartForm(1 << 20); err != nil {
		status := 400
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			status = 413
		}
		c.JSON(status, gin.H{"error": gin.H{"message": "Invalid multipart upload or file exceeds 20 MB"}})
		return
	}
	defer c.Request.MultipartForm.RemoveAll()
	file, header, err := c.Request.FormFile("file")
	if err != nil {
		c.JSON(400, gin.H{"error": gin.H{"message": "A multipart file field named file is required"}})
		return
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxBytes+1))
	if err != nil || len(data) == 0 {
		c.JSON(400, gin.H{"error": gin.H{"message": "Unable to read image file"}})
		return
	}
	if len(data) > maxBytes {
		c.JSON(413, gin.H{"error": gin.H{"message": "Image upload must not exceed 20 MB"}})
		return
	}
	contentType := http.DetectContentType(data)
	switch contentType {
	case "image/jpeg", "image/png", "image/gif", "image/webp":
	default:
		c.JSON(400, gin.H{"error": gin.H{"message": "Supported upload formats are JPEG, PNG, GIF, and WebP"}})
		return
	}
	imageURL, err := service.StoreUploadedMediaImage(data, contentType)
	if err != nil {
		seedanceLibraryError(c, err)
		return
	}
	filename := filepath.Base(strings.ReplaceAll(header.Filename, "\\", "/"))
	c.JSON(200, gin.H{"url": imageURL, "filename": filename, "content_type": contentType, "bytes": len(data), "created_at": time.Now().Unix()})
}
