package service

import (
	"github.com/QuantumNous/new-api/model"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// SQL diagnostics must not serialize H5 credentials or signed portrait URLs.
func seedanceDB() *gorm.DB {
	return model.DB.Session(&gorm.Session{Logger: logger.Default.LogMode(logger.Silent)})
}
