package service

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"github.com/QuantumNous/new-api/model"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Bridge only this owner's canonical, unexpired, signed private upload. Never
// fetch a caller URL or interpret an arbitrary URL as a filesystem path.
func videoFeePrivateUpload(asset *model.SeedanceResource) (*seedanceMediaUpload, error) {
	origin := strings.TrimRight(os.Getenv("SEEDANCE_CALLBACK_ORIGIN"), "/")
	prefix := origin + "/v1/seedance2/private-avatar/files/"
	if origin == "" || !strings.HasPrefix(asset.SourceURL, prefix) {
		return nil, nil
	}
	parts := strings.Split(strings.TrimPrefix(asset.SourceURL, prefix), "/")
	dir, key := os.Getenv("SEEDANCE_PORTRAIT_DIR"), os.Getenv("SEEDANCE_PORTRAIT_SIGNING_KEY")
	if len(parts) != 3 || !filepath.IsAbs(dir) || len(key) < 32 {
		return nil, seedanceError(400, "Controlled portrait upload is unavailable")
	}
	id, expires, signature := parts[0], parts[1], parts[2]
	bits := strings.Split(id, "_")
	if len(bits) != 2 || bits[0] != strconv.Itoa(asset.UserID) || len(bits[1]) != 48 {
		return nil, seedanceError(403, "Portrait upload does not belong to this user")
	}
	if _, err := hex.DecodeString(bits[1]); err != nil {
		return nil, seedanceError(400, "Invalid portrait upload")
	}
	deadline, err := strconv.ParseInt(expires, 10, 64)
	mac := hmac.New(sha256.New, []byte(key))
	mac.Write([]byte(id + ":" + expires))
	if err != nil || deadline <= time.Now().Unix() || deadline > time.Now().Unix()+3600 || !hmac.Equal([]byte(signature), []byte(hex.EncodeToString(mac.Sum(nil)))) {
		return nil, seedanceError(400, "Portrait upload link is invalid or expired")
	}
	file, err := os.Open(filepath.Join(dir, id))
	if err != nil {
		return nil, seedanceError(400, "Portrait upload is unavailable")
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || time.Since(info.ModTime()) >= 24*time.Hour {
		return nil, seedanceError(400, "Portrait upload has expired")
	}
	data, err := io.ReadAll(io.LimitReader(file, (10<<20)+1))
	kind := http.DetectContentType(data)
	if err != nil || len(data) > 10<<20 || (kind != "image/jpeg" && kind != "image/png") {
		return nil, seedanceError(400, "Invalid portrait image")
	}
	ext := ".jpg"
	if kind == "image/png" {
		ext = ".png"
	}
	return &seedanceMediaUpload{filename: "portrait" + ext, data: data}, nil
}
