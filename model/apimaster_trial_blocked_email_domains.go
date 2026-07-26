package model

import (
	"errors"
	"regexp"
	"strings"
)

var trialBlockedEmailDomainPattern = regexp.MustCompile(`^[a-z0-9.-]+\.[a-z]{2,}$`)

func normalizeTrialBlockedEmailDomain(domain string) string {
	return strings.TrimPrefix(strings.ToLower(strings.TrimSpace(domain)), "@")
}

func validateTrialBlockedEmailDomain(domain string) error {
	if !trialBlockedEmailDomainPattern.MatchString(domain) {
		return errors.New("邮箱域名格式无效")
	}
	return nil
}

func ensureApimasterTrialBlockedEmailDomainSchema() error {
	if APIMASTER_PG_DB == nil {
		return errors.New("APIMASTER_PG_DSN 未配置")
	}
	return APIMASTER_PG_DB.Exec(`
		CREATE TABLE IF NOT EXISTS trial_blocked_email_domains (
			domain text PRIMARY KEY,
			created_at timestamptz NOT NULL DEFAULT now(),
			updated_at timestamptz NOT NULL DEFAULT now()
		)
	`).Error
}

func AddTrialBlockedEmailDomain(domain string) (string, error) {
	if err := ensureApimasterTrialBlockedEmailDomainSchema(); err != nil {
		return "", err
	}
	normalized := normalizeTrialBlockedEmailDomain(domain)
	if err := validateTrialBlockedEmailDomain(normalized); err != nil {
		return "", err
	}
	err := APIMASTER_PG_DB.Exec(`
		INSERT INTO trial_blocked_email_domains (domain, updated_at)
		VALUES (?, now())
		ON CONFLICT (domain) DO UPDATE
		SET updated_at = now()
	`, normalized).Error
	if err != nil {
		return "", err
	}
	return normalized, nil
}

func RemoveTrialBlockedEmailDomain(domain string) (string, error) {
	if err := ensureApimasterTrialBlockedEmailDomainSchema(); err != nil {
		return "", err
	}
	normalized := normalizeTrialBlockedEmailDomain(domain)
	if err := validateTrialBlockedEmailDomain(normalized); err != nil {
		return "", err
	}
	err := APIMASTER_PG_DB.Exec(`
		DELETE FROM trial_blocked_email_domains
		WHERE domain = ?
	`, normalized).Error
	if err != nil {
		return "", err
	}
	return normalized, nil
}
