package model

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
)

func TestTopupHistoryDateScopeSummaryAndExport(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	old := DB
	DB = db
	t.Cleanup(func() { DB = old })
	require.NoError(t, db.AutoMigrate(&TopUp{}, &SubscriptionOrder{}, &User{}))
	from, until, err := ParseTopupHistoryDates("2026-09-01", "2026-09-01")
	require.NoError(t, err)
	require.EqualValues(t, 86400, until-from)
	require.NoError(t, db.Create(&[]TopUp{
		{UserId: 1, TradeNo: "start", CreateTime: from, Amount: 5, Money: 417.75, PaymentMethod: "platega", Status: "success"},
		{UserId: 1, TradeNo: "end", CreateTime: until - 1, CreditedAmount: 1.234567, PaymentMethod: "platega", Status: "success"},
		{UserId: 1, TradeNo: "before", CreateTime: from - 1, Amount: 99, Status: "success"},
		{UserId: 1, TradeNo: "after", CreateTime: until, Amount: 99, Status: "success"},
		{UserId: 1, TradeNo: "failed", CreateTime: from + 1, Amount: 100, Status: "failed"},
		{UserId: 1, TradeNo: "pending", CreateTime: from + 1, Amount: 100, Status: "pending"},
		{UserId: 1, TradeNo: "refunded", CreateTime: from + 1, Amount: 100, Status: "refunded"},
		{UserId: 1, TradeNo: "subscription", CreateTime: from + 1, CreditedAmount: 69, Status: "success"},
		{UserId: 1, TradeNo: "free", CreateTime: from + 1, CreditedAmount: 999, PaymentMethod: "free", Status: "success"},
		{UserId: 2, TradeNo: "other-user", CreateTime: from + 1, Amount: 3, Status: "success"},
	}).Error)
	require.NoError(t, db.Create(&SubscriptionOrder{TradeNo: "subscription", UserId: 1, Status: "success"}).Error)
	f := TopupHistoryFilter{StartTime: from, EndTime: until}
	rows, total, summary, err := QueryTopupHistory(f, &common.PageInfo{Page: 1, PageSize: 1})
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.EqualValues(t, 8, total)
	require.EqualValues(t, 3, summary.Count)
	require.Equal(t, "9.234567", summary.RechargeUSD)
	f.Keyword = "1" // exact admin UID search
	_, total, summary, err = QueryTopupHistory(f, &common.PageInfo{Page: 2, PageSize: 1})
	require.NoError(t, err)
	require.EqualValues(t, 7, total)
	require.Equal(t, "6.234567", summary.RechargeUSD)
	f.PaymentMethod = "platega"
	f.Status = "success"
	exported, err := ExportTopupHistory(f)
	require.NoError(t, err)
	require.Len(t, exported, 2)
	_, total, summary, err = QueryTopupHistory(f, &common.PageInfo{Page: 1, PageSize: 1})
	require.NoError(t, err)
	require.EqualValues(t, 2, total)
	require.EqualValues(t, 2, summary.Count)
	f.Status = "pending"
	_, total, summary, err = QueryTopupHistory(f, &common.PageInfo{Page: 1, PageSize: 1})
	require.NoError(t, err)
	require.Zero(t, total)
	require.Equal(t, "0", summary.RechargeUSD)
}

func TestTopupHistoryDatesAndUserIsolation(t *testing.T) {
	for _, dates := range [][2]string{{"bad", ""}, {"2026-02-30", ""}, {"2026-10-02", "2026-10-01"}} {
		_, _, err := ParseTopupHistoryDates(dates[0], dates[1])
		require.Error(t, err)
	}
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	old := DB
	DB = db
	t.Cleanup(func() { DB = old })
	require.NoError(t, db.AutoMigrate(&TopUp{}, &SubscriptionOrder{}))
	now := common.GetTimestamp()
	require.NoError(t, db.Create(&[]TopUp{
		{UserId: 1, TradeNo: "mine", Amount: 5, CreateTime: now, Status: "success"},
		{UserId: 2, TradeNo: "other", Amount: 100, CreateTime: now, Status: "success"},
		{UserId: 1, TradeNo: "old", Amount: 100, CreateTime: now - 31*86400, Status: "success"},
	}).Error)
	rows, total, summary, err := QueryTopupHistory(TopupHistoryFilter{UserID: 1}, &common.PageInfo{Page: 1, PageSize: 10})
	require.NoError(t, err)
	require.EqualValues(t, 1, total)
	require.Equal(t, "mine", rows[0].TradeNo)
	require.Equal(t, "5", summary.RechargeUSD)
}
