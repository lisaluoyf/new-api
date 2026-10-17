package service

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"

	"github.com/QuantumNous/new-api/common"
)

// ParseSeedanceResponseDuration accepts provider seconds as a JSON number or
// numeric string, retaining fractional seconds for the billing receipt. Invalid
// reported values must never become a zero or a requested-duration fallback.
func ParseSeedanceResponseDuration(raw []byte) (float64, error) {
	var value json.Number
	if err := common.Unmarshal(raw, &value); err != nil || value.String() == "" {
		return 0, fmt.Errorf("invalid provider duration: expected numeric seconds")
	}
	seconds, err := strconv.ParseFloat(value.String(), 64)
	if err != nil || math.IsNaN(seconds) || math.IsInf(seconds, 0) || seconds <= 0 || seconds > math.MaxInt32 || math.Round(seconds) < 1 {
		return 0, fmt.Errorf("invalid provider duration: expected positive finite seconds within billing range")
	}
	return seconds, nil
}
