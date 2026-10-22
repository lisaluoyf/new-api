package controller

import "github.com/QuantumNous/new-api/types"

// Share policy attribution with channel health and log categorization.
func isContentSafetyRejection(code, errorType, message string) bool {
	return types.IsContentSafetyRejection(code, errorType, message)
}
