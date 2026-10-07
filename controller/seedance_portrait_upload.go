package controller

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

func portraitStore() (string, string, bool) {
	dir, key := os.Getenv("SEEDANCE_PORTRAIT_DIR"), os.Getenv("SEEDANCE_PORTRAIT_SIGNING_KEY")
	return dir, key, filepath.IsAbs(dir) && len(key) >= 32
}
func portraitSignature(key, id, expires string) string {
	mac := hmac.New(sha256.New, []byte(key))
	mac.Write([]byte(id + ":" + expires))
	return hex.EncodeToString(mac.Sum(nil))
}

// Originals are separate from reviewed upstream assets. Signed access lasts one
// hour; local originals expire after 24h and must be pruned by the documented timer.
func UploadSeedancePortrait(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	dir, key, ok := portraitStore()
	origin := strings.TrimRight(os.Getenv("SEEDANCE_CALLBACK_ORIGIN"), "/")
	if !ok || !strings.HasPrefix(origin, "https://") {
		c.JSON(503, gin.H{"error": gin.H{"message": "Controlled portrait storage is not configured"}})
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 11<<20)
	file, err := c.FormFile("file")
	if err != nil || file.Size > 10<<20 {
		c.JSON(400, gin.H{"error": gin.H{"message": "Upload one JPEG/PNG image up to 10 MB"}})
		return
	}
	if c.Request.MultipartForm != nil {
		defer c.Request.MultipartForm.RemoveAll()
	}
	source, err := file.Open()
	if err != nil {
		c.AbortWithStatus(400)
		return
	}
	defer source.Close()
	data, err := io.ReadAll(io.LimitReader(source, (10<<20)+1))
	kind := http.DetectContentType(data)
	if err != nil || len(data) > 10<<20 || (kind != "image/jpeg" && kind != "image/png") {
		c.JSON(400, gin.H{"error": gin.H{"message": "Invalid portrait image"}})
		return
	}
	var nonce [24]byte
	if _, err = rand.Read(nonce[:]); err != nil {
		c.AbortWithStatus(500)
		return
	}
	id := fmt.Sprintf("%d_%s", c.GetInt("id"), hex.EncodeToString(nonce[:]))
	if os.MkdirAll(dir, 0700) != nil || os.WriteFile(filepath.Join(dir, id), data, 0600) != nil {
		c.AbortWithStatus(500)
		return
	}
	expires := strconv.FormatInt(time.Now().Unix()+3600, 10)
	signature := portraitSignature(key, id, expires)
	c.JSON(200, gin.H{"code": 200, "data": gin.H{"upload_id": id, "url": origin + "/v1/seedance2/private-avatar/files/" + id + "/" + expires + "/" + signature, "url_expires_at": time.Now().Unix() + 3600, "delete_after": time.Now().Unix() + 86400}})
}
func validPortraitID(id string) bool {
	parts := strings.Split(id, "_")
	if len(parts) != 2 || len(parts[1]) != 48 {
		return false
	}
	_, e := strconv.Atoi(parts[0])
	_, e2 := hex.DecodeString(parts[1])
	return e == nil && e2 == nil
}
func DownloadSeedancePortrait(c *gin.Context) {
	c.Header("Cache-Control", "no-store, private")
	c.Header("Referrer-Policy", "no-referrer")
	c.Header("X-Content-Type-Options", "nosniff")
	dir, key, ok := portraitStore()
	id, expires := c.Param("upload_id"), c.Param("expires")
	expiry, e := strconv.ParseInt(expires, 10, 64)
	if !ok || !validPortraitID(id) || e != nil || expiry <= time.Now().Unix() || expiry > time.Now().Unix()+3600 || !hmac.Equal([]byte(c.Param("signature")), []byte(portraitSignature(key, id, expires))) {
		c.AbortWithStatus(404)
		return
	}
	info, e := os.Stat(filepath.Join(dir, id))
	if e != nil || time.Since(info.ModTime()) >= 24*time.Hour {
		c.AbortWithStatus(404)
		return
	}
	body, e := os.ReadFile(filepath.Join(dir, id))
	if e != nil {
		c.AbortWithStatus(404)
		return
	}
	c.Data(200, http.DetectContentType(body), body)
}
func DeleteSeedancePortraitUpload(c *gin.Context) {
	dir, _, ok := portraitStore()
	id := c.Param("upload_id")
	if !ok || !validPortraitID(id) || !strings.HasPrefix(id, strconv.Itoa(c.GetInt("id"))+"_") {
		c.AbortWithStatus(404)
		return
	}
	if err := os.Remove(filepath.Join(dir, id)); err != nil && !os.IsNotExist(err) {
		c.AbortWithStatus(500)
		return
	}
	c.JSON(200, gin.H{"code": 200, "data": gin.H{"deleted": true, "upload_id": id}})
}
