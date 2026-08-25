package model

import (
	"time"

	"gorm.io/gorm/clause"
)

// DiscordGroupVerification stores the latest real-time membership snapshot
// for an APIMaster user in a Discord guild.
type DiscordGroupVerification struct {
	ID        int        `json:"id" gorm:"primaryKey"`
	UserID    int        `json:"user_id" gorm:"column:user_id;not null;uniqueIndex:idx_discord_group_user_guild"`
	GuildID   string     `json:"guild_id" gorm:"column:guild_id;type:varchar(32);not null;uniqueIndex:idx_discord_group_user_guild"`
	JoinedAt  *time.Time `json:"joined_at" gorm:"column:joined_at"`
	CheckedAt time.Time  `json:"checked_at" gorm:"column:checked_at;not null;index"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
}

func (DiscordGroupVerification) TableName() string {
	return "discord_group_verifications"
}

func GetDiscordGroupVerification(userID int, guildID string) (*DiscordGroupVerification, error) {
	var verification DiscordGroupVerification
	err := DB.Where("user_id = ? AND guild_id = ?", userID, guildID).
		First(&verification).Error
	return &verification, err
}

// RecordDiscordGroupVerification upserts the current membership state. The
// first successful check records joined_at; leaving clears it so a later
// rejoin receives a fresh timestamp.
func RecordDiscordGroupVerification(userID int, guildID string, joined bool, now time.Time) error {
	checkedAt := now.UTC()
	var joinedAt *time.Time
	if joined {
		joinedAt = &checkedAt
	}
	verification := DiscordGroupVerification{
		UserID:    userID,
		GuildID:   guildID,
		JoinedAt:  joinedAt,
		CheckedAt: checkedAt,
	}
	joinedAtUpdate := interface{}(nil)
	if joined {
		joinedAtUpdate = gormExprCoalesce("joined_at", checkedAt)
	}
	return DB.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "user_id"}, {Name: "guild_id"}},
		DoUpdates: clause.Assignments(map[string]interface{}{
			"joined_at":  joinedAtUpdate,
			"checked_at": checkedAt,
			"updated_at": checkedAt,
		}),
	}).Create(&verification).Error
}

func gormExprCoalesce(column string, fallback time.Time) clause.Expr {
	return clause.Expr{SQL: "COALESCE(" + column + ", ?)", Vars: []interface{}{fallback}}
}
