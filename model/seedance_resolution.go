package model

import (
	"fmt"
	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm/clause"
	"slices"
	"strings"
)

// An absent row enables all variants; a saved empty list disables the route.
type SeedanceChannelResolution struct {
	ID           int64  `gorm:"primaryKey"`
	ChannelID    int    `gorm:"uniqueIndex:seedance_resolution_route"`
	Model        string `gorm:"type:varchar(64);uniqueIndex:seedance_resolution_route"`
	VariantsJSON string `gorm:"type:text"`
}

func SeedanceResolutionOptions(name string) []string {
	name = NormalizeVerifiedVideoModel(name)
	if name == "" {
		return nil
	}
	resolutions := []string{"480P", "720P"}
	if name != "seedance-2.0-fast" && name != "seedance-2.0-mini" {
		resolutions = append(resolutions, "1080P")
	}
	if name == "seedance-2.0" {
		resolutions = append(resolutions, "4K")
	}
	result := make([]string, 0, len(resolutions)*2)
	for _, value := range resolutions {
		result = append(result, value, value+"-input")
	}
	return result
}

func SeedanceResolutionSelections(name string) (map[int][]string, error) {
	var rows []SeedanceChannelResolution
	if err := DB.Where("model = ?", NormalizeVerifiedVideoModel(name)).Find(&rows).Error; err != nil {
		return nil, err
	}
	result := make(map[int][]string, len(rows))
	for _, row := range rows {
		var values []string
		if err := common.UnmarshalJsonStr(row.VariantsJSON, &values); err != nil {
			return nil, err
		}
		result[row.ChannelID] = values
	}
	return result, nil
}

func SaveSeedanceResolutionSelection(channelID int, name string, values []string) error {
	name = NormalizeVerifiedVideoModel(name)
	options := SeedanceResolutionOptions(name)
	if channelID <= 0 || len(options) == 0 || values == nil {
		return fmt.Errorf("Invalid Seedance resolution selection")
	}
	selected := make([]string, 0, len(values))
	for _, value := range values {
		index := slices.IndexFunc(options, func(option string) bool { return strings.EqualFold(option, value) })
		if index < 0 {
			return fmt.Errorf("Unsupported resolution: %s", value)
		}
		if !slices.Contains(selected, options[index]) {
			selected = append(selected, options[index])
		}
	}
	encoded, err := common.Marshal(selected)
	if err != nil {
		return err
	}
	row := SeedanceChannelResolution{ChannelID: channelID, Model: name, VariantsJSON: string(encoded)}
	return DB.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "channel_id"}, {Name: "model"}}, DoUpdates: clause.AssignmentColumns([]string{"variants_json"})}).Create(&row).Error
}
