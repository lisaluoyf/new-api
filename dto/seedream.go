package dto

import "strings"

const Seedream5ProModel = "doubao-seedream-5-0-pro-260628"

func IsSeedream5Pro(model string) bool {
	return strings.EqualFold(strings.TrimSpace(model), Seedream5ProModel)
}
