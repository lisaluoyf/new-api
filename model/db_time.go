package model

import (
	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

// GetDBTimestamp returns a UNIX timestamp from database time.
// Falls back to application time on error.
func GetDBTimestamp() int64 {
	return GetDBTimestampTx(nil)
}

// GetDBTimestampTx reads database time through the current transaction when
// one is available. This avoids acquiring a second connection while a payment
// transaction is holding SQLite's only connection.
func GetDBTimestampTx(tx *gorm.DB) int64 {
	var ts int64
	var err error
	query := DB
	if tx != nil {
		query = tx
	}
	switch {
	case common.UsingPostgreSQL:
		err = query.Raw("SELECT EXTRACT(EPOCH FROM NOW())::bigint").Scan(&ts).Error
	case common.UsingSQLite:
		err = query.Raw("SELECT strftime('%s','now')").Scan(&ts).Error
	default:
		err = query.Raw("SELECT UNIX_TIMESTAMP()").Scan(&ts).Error
	}
	if err != nil || ts <= 0 {
		return common.GetTimestamp()
	}
	return ts
}
