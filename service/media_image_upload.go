package service

import (
	"bytes"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"os"
	"path/filepath"

	"github.com/QuantumNous/new-api/model"
	_ "golang.org/x/image/webp"
)

func StoreUploadedMediaImage(data []byte, contentType string) (string, error) {
	config, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || config.Width < 1 || config.Height < 1 || int64(config.Width)*int64(config.Height) > 100000000 {
		return "", seedanceError(400, "Invalid image content or dimensions")
	}
	ext := map[string]string{"image/jpeg": ".jpg", "image/png": ".png", "image/gif": ".gif", "image/webp": ".webp"}[contentType]
	if ext == "" {
		return "", seedanceError(400, "Unsupported image format")
	}
	if err := os.MkdirAll(imageCacheDir, 0755); err != nil {
		return "", err
	}
	filename := "media_upload_" + model.GenerateTaskID() + ext
	if err := os.WriteFile(filepath.Join(imageCacheDir, filename), data, 0644); err != nil {
		return "", err
	}
	return imageCachePublicBase + filename, nil
}
