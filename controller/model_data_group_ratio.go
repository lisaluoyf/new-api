package controller

import "github.com/QuantumNous/new-api/service"

func applyModelGroupRatioToRow(setting *string, modelName string,
	inputPrice, outputPrice, cachePrice, cacheCreationPrice, groupRatio **float64,
) {
	row := service.ChannelPricingLookupRow{}
	fields := []struct {
		pointer **float64
		value   *float64
	}{
		{inputPrice, &row.InputPrice}, {outputPrice, &row.OutputPrice},
		{cachePrice, &row.CachePrice}, {cacheCreationPrice, &row.CacheCreationPrice},
		{groupRatio, &row.GroupRatio},
	}
	for _, field := range fields {
		if field.pointer != nil && *field.pointer != nil {
			*field.value = **field.pointer
		}
	}
	service.ApplyModelGroupRatio(setting, modelName, &row)
	for _, field := range fields {
		if field.pointer != nil && *field.pointer != nil {
			// Replace pointers rather than modifying a shared SQL result.
			value := *field.value
			*field.pointer = &value
		}
	}
}
