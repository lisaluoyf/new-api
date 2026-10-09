package controller

import (
	"context"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

func VerifySeedanceVideo(c *gin.Context) {
	const maxBytes int64 = 500 * 1024 * 1024
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBytes+1024*1024)
	ctx, cancel := context.WithTimeout(c.Request.Context(), 180*time.Second)
	defer cancel()
	var result *service.SeedanceVerificationResponse
	var err error
	if strings.Contains(c.GetHeader("Content-Type"), "multipart/form-data") {
		if err = c.Request.ParseMultipartForm(8 * 1024 * 1024); err != nil {
			c.JSON(413, gin.H{"error": "Invalid or oversized multipart upload"})
			return
		}
		defer c.Request.MultipartForm.RemoveAll()
		name := c.Request.FormValue("model")
		if model.NormalizeVerifiedVideoModel(name) == "" {
			c.JSON(400, gin.H{"error": "Unsupported Seedance model"})
			return
		}
		upload, header, uploadErr := c.Request.FormFile("file")
		if uploadErr != nil || header.Size > maxBytes {
			c.JSON(400, gin.H{"error": "A video file no larger than 500 MB is required"})
			return
		}
		defer upload.Close()
		file, fileErr := os.CreateTemp("", "apm-verify-api-*.mp4")
		if fileErr != nil {
			c.JSON(503, gin.H{"error": "Temporary storage unavailable"})
			return
		}
		defer func() { file.Close(); os.Remove(file.Name()) }()
		if _, err = io.Copy(file, io.LimitReader(upload, maxBytes+1)); err != nil {
			c.JSON(400, gin.H{"error": "Video upload failed"})
			return
		}
		if _, err = file.Seek(0, 0); err != nil {
			c.JSON(503, gin.H{"error": "Temporary storage unavailable"})
			return
		}
		result, err = service.VerifySeedanceVideoFile(ctx, file, name)
	} else {
		var request struct {
			Model    string `json:"model"`
			VideoURL string `json:"video_url"`
		}
		if c.ShouldBindJSON(&request) != nil || model.NormalizeVerifiedVideoModel(request.Model) == "" || request.VideoURL == "" {
			c.JSON(400, gin.H{"error": "model and video_url are required"})
			return
		}
		result, err = service.VerifySeedanceVideoURL(ctx, request.VideoURL, request.Model)
	}
	if err != nil {
		c.JSON(502, gin.H{"error": "Video could not be downloaded or verified"})
		return
	}
	c.JSON(200, result)
}
