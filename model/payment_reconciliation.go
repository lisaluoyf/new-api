package model

import (
	"errors"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// PaymentQueryReference contains query identifiers only, never credentials or
// raw provider responses. Older orders without identifiers remain unverified.
type PaymentQueryReference struct {
	ID        int    `json:"id"`
	TradeNo   string `gorm:"type:varchar(255);uniqueIndex" json:"trade_no"`
	Provider  string `gorm:"type:varchar(32)" json:"provider"`
	QueryID   string `gorm:"type:varchar(255)" json:"query_id"`
	Currency  string `gorm:"type:varchar(16)" json:"currency"`
	UpdatedAt int64  `json:"updated_at"`
}

func SavePaymentQueryReference(trade, provider, id, currency string) error {
	if trade == "" || provider == "" || id == "" {
		return errors.New("missing payment query reference")
	}
	r := PaymentQueryReference{TradeNo: trade, Provider: provider, QueryID: id, Currency: strings.ToUpper(currency), UpdatedAt: time.Now().Unix()}
	return DB.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "trade_no"}}, DoUpdates: clause.AssignmentColumns([]string{"provider", "query_id", "currency", "updated_at"})}).Create(&r).Error
}

type PaymentReconciliationJob struct {
	ActorID     int    `json:"actor_id"`
	ID          int    `json:"id"`
	Day         string `gorm:"type:varchar(10);uniqueIndex:uk_payment_reconciliation_day_provider,priority:1" json:"day"`
	Provider    string `gorm:"type:varchar(32);uniqueIndex:uk_payment_reconciliation_day_provider,priority:2" json:"provider"`
	Status      string `gorm:"type:varchar(24);index" json:"status"`
	LeaseUntil  int64  `json:"lease_until"`
	RunID       int    `json:"run_id"`
	Attempts    int    `json:"attempts"`
	NextAttempt int64  `json:"next_attempt"`
}
type PaymentReconciliationRun struct {
	ID                int                          `json:"id"`
	Day               string                       `gorm:"type:varchar(10);index" json:"day"`
	Provider          string                       `gorm:"type:varchar(32);index" json:"provider"`
	Status            string                       `gorm:"type:varchar(24)" json:"status"`
	ActorID           int                          `json:"actor_id"`
	StartedAt         int64                        `json:"started_at"`
	FinishedAt        int64                        `json:"finished_at"`
	CheckedCount      int                          `json:"checked_count"`
	LocalPaidCount    int                          `json:"local_paid_count"`
	OfficialPaidCount int                          `json:"official_paid_count"`
	DifferenceCount   int                          `json:"difference_count"`
	UnverifiedCount   int                          `json:"unverified_count"`
	TotalsJSON        string                       `gorm:"type:text" json:"-"`
	Totals            []PaymentReconciliationTotal `gorm:"-" json:"totals"`
	Coverage          string                       `gorm:"type:varchar(64)" json:"coverage"`
}
type PaymentReconciliationTotal struct {
	Currency       string `json:"currency"`
	LocalAmount    string `json:"local_amount"`
	OfficialAmount string `json:"official_amount"`
	Difference     string `json:"difference"`
}
type PaymentReconciliationItem struct {
	ID               int    `json:"id"`
	RunID            int    `gorm:"index" json:"run_id"`
	TradeNo          string `gorm:"type:varchar(255);index" json:"trade_no"`
	UserID           int    `json:"user_id"`
	Purpose          string `gorm:"type:varchar(32)" json:"purpose"`
	LocalStatus      string `gorm:"type:varchar(32)" json:"local_status"`
	LocalPaid        bool   `json:"local_paid"`
	LocalAmount      string `gorm:"type:varchar(64)" json:"local_amount"`
	Currency         string `gorm:"type:varchar(16)" json:"currency"`
	OfficialID       string `gorm:"type:varchar(255)" json:"official_id"`
	OfficialStatus   string `gorm:"type:varchar(64)" json:"official_status"`
	OfficialPaid     bool   `json:"official_paid"`
	OfficialAmount   string `gorm:"type:varchar(64)" json:"official_amount"`
	OfficialCurrency string `gorm:"type:varchar(16)" json:"official_currency"`
	Result           string `gorm:"type:varchar(24);index" json:"result"`
	Problem          string `gorm:"type:varchar(96)" json:"problem"`
	CheckedAt        int64  `json:"checked_at"`
}

// Only currently enabled channels participate in reconciliation and its summaries.
var PaymentReconciliationProviders = []string{"epay", "platega", "paypal", "clink", "waffo_pancake", "nowpayments", "crypto"}

func PaymentReconciliationProviderEnabled(provider string) bool {
	for _, p := range PaymentReconciliationProviders {
		if p == provider {
			return true
		}
	}
	return false
}

func QueuePaymentReconciliation(day, provider string, actor int, force bool) error {
	if !PaymentReconciliationProviderEnabled(provider) {
		return errors.New("payment reconciliation provider is disabled")
	}
	return DB.Transaction(func(tx *gorm.DB) error {
		job := PaymentReconciliationJob{Day: day, Provider: provider, Status: "queued", ActorID: actor}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&job).Error; err != nil {
			return err
		}
		if !force {
			return nil
		}
		var current PaymentReconciliationJob
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("day = ? AND provider = ?", day, provider).First(&current).Error; err != nil {
			return err
		}
		if current.Status == "running" && current.LeaseUntil > time.Now().Unix() {
			return errors.New("reconciliation already running")
		}
		// The requester is copied to the immutable run when the worker claims it.
		return tx.Model(&current).Updates(map[string]any{"status": "queued", "attempts": 0, "next_attempt": 0, "lease_until": 0, "actor_id": actor}).Error
	})
}
func ClaimPaymentReconciliationJob(now int64) (*PaymentReconciliationJob, error) {
	var claimed *PaymentReconciliationJob
	err := DB.Transaction(func(tx *gorm.DB) error {
		var j PaymentReconciliationJob
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("provider IN ?", PaymentReconciliationProviders).Where("(status = ? AND next_attempt <= ?) OR (status = ? AND lease_until <= ?)", "queued", now, "running", now).Order("day DESC, id ASC").First(&j).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		if j.Status == "running" && j.RunID > 0 {
			if err := tx.Model(&PaymentReconciliationRun{}).Where("id = ? AND status = ?", j.RunID, "running").Updates(map[string]any{"status": "interrupted", "finished_at": now}).Error; err != nil {
				return err
			}
		}
		r := PaymentReconciliationRun{Day: j.Day, Provider: j.Provider, Status: "running", ActorID: j.ActorID, StartedAt: now, Coverage: "local_orders_queried_against_provider"}
		if err := tx.Create(&r).Error; err != nil {
			return err
		}
		upd := tx.Model(&PaymentReconciliationJob{}).Where("id = ? AND status = ? AND lease_until = ?", j.ID, j.Status, j.LeaseUntil).Updates(map[string]any{"status": "running", "lease_until": now + 3600, "run_id": r.ID, "attempts": j.Attempts + 1})
		if upd.Error != nil {
			return upd.Error
		}
		if upd.RowsAffected != 1 {
			return errors.New("reconciliation claim lost")
		}
		j.Status = "running"
		j.RunID = r.ID
		j.Attempts++
		j.LeaseUntil = now + 3600
		claimed = &j
		return nil
	})
	return claimed, err
}
func HeartbeatPaymentReconciliation(j *PaymentReconciliationJob, now int64) error {
	r := DB.Model(&PaymentReconciliationJob{}).Where("id = ? AND run_id = ? AND status = ?", j.ID, j.RunID, "running").Update("lease_until", now+3600)
	if r.Error != nil {
		return r.Error
	}
	if r.RowsAffected != 1 {
		return errors.New("reconciliation lease lost")
	}
	return nil
}
func FinishPaymentReconciliation(j *PaymentReconciliationJob, items []PaymentReconciliationItem, runError bool) error {
	r := SummarizePaymentReconciliation(items)
	r.ID = j.RunID
	r.FinishedAt = time.Now().Unix()
	if runError {
		r.Status = "incomplete"
	}
	b, err := common.Marshal(r.Totals)
	if err != nil {
		return err
	}
	r.TotalsJSON = string(b)
	return DB.Transaction(func(tx *gorm.DB) error {
		var lock PaymentReconciliationJob
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", j.ID).First(&lock).Error; err != nil {
			return err
		}
		if lock.RunID != j.RunID || lock.Status != "running" {
			return errors.New("stale reconciliation result")
		}
		for i := range items {
			items[i].ID = 0
			items[i].RunID = j.RunID
		}
		if len(items) > 0 {
			if err := tx.CreateInBatches(items, 100).Error; err != nil {
				return err
			}
		}
		if err := tx.Model(&PaymentReconciliationRun{}).Where("id = ?", j.RunID).Updates(map[string]any{"status": r.Status, "finished_at": r.FinishedAt, "checked_count": r.CheckedCount, "local_paid_count": r.LocalPaidCount, "official_paid_count": r.OfficialPaidCount, "difference_count": r.DifferenceCount, "unverified_count": r.UnverifiedCount, "totals_json": r.TotalsJSON, "coverage": r.Coverage}).Error; err != nil {
			return err
		}
		status := "done"
		next := int64(0)
		if r.UnverifiedCount > 0 && j.Attempts < 3 {
			status = "queued"
			next = r.FinishedAt + 300
		}
		return tx.Model(&lock).Updates(map[string]any{"status": status, "next_attempt": next, "lease_until": 0}).Error
	})
}
func SummarizePaymentReconciliation(items []PaymentReconciliationItem) PaymentReconciliationRun {
	r := PaymentReconciliationRun{CheckedCount: len(items), Status: "matched", Coverage: "bidirectional_official_statement", Totals: []PaymentReconciliationTotal{}}
	local := map[string]decimal.Decimal{}
	official := map[string]decimal.Decimal{}
	for _, i := range items {
		if i.Purpose == "coverage" {
			r.Coverage = "local_orders_only_statement_incomplete"
			r.CheckedCount--
		}
		if i.LocalPaid {
			r.LocalPaidCount++
			a, e := decimal.NewFromString(i.LocalAmount)
			if e == nil {
				local[i.Currency] = local[i.Currency].Add(a)
			}
		}
		if i.OfficialPaid {
			r.OfficialPaidCount++
			a, e := decimal.NewFromString(i.OfficialAmount)
			if e == nil {
				official[i.OfficialCurrency] = official[i.OfficialCurrency].Add(a)
			}
		}
		if i.Result == "difference" {
			r.DifferenceCount++
		}
		if i.Result == "unverified" {
			r.UnverifiedCount++
		}
	}
	keys := map[string]bool{}
	for k := range local {
		keys[k] = true
	}
	for k := range official {
		keys[k] = true
	}
	for k := range keys {
		r.Totals = append(r.Totals, PaymentReconciliationTotal{Currency: k, LocalAmount: local[k].String(), OfficialAmount: official[k].String(), Difference: local[k].Sub(official[k]).String()})
	}
	if r.DifferenceCount > 0 {
		r.Status = "difference"
	}
	if r.UnverifiedCount > 0 {
		r.Status = "incomplete"
	}
	return r
}
