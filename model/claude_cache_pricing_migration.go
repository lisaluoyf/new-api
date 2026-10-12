package model

import (
	"errors"
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const claudeCachePricingMigrationKey = "ClaudeCachePricingMigrationV1"

func migrateClaudeCachePricing() error {
	var marker Option
	err := DB.Where(&Option{Key: claudeCachePricingMigrationKey}).First(&marker).Error
	if err == nil && marker.Value == "1" {
		return nil
	}
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	ratios := ratio_setting.GetCacheRatioCopy()
	for _, name := range []string{"claude-sonnet-5-5", "claude-sonnet-5.5"} {
		old, ok := ratios[name]
		if !ok || old == 0.1 {
			ratios[name] = 0.05
		}
	}
	raw, err := common.Marshal(ratios)
	if err != nil {
		return err
	}
	if err := DB.Transaction(func(tx *gorm.DB) error {
		return tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "key"}}, DoUpdates: clause.AssignmentColumns([]string{"value"})}).Create(&[]Option{{Key: "CacheRatio", Value: string(raw)}, {Key: claudeCachePricingMigrationKey, Value: "1"}}).Error
	}); err != nil {
		return err
	}
	return updateOptionMap("CacheRatio", string(raw))
}
