package controller

import "github.com/QuantumNous/new-api/service"

func applyModelGroupRatioToRow(setting *string, modelName string, inputPrice, outputPrice, cachePrice, cacheCreationPrice, groupRatio **float64) {
	service.ApplyModelGroupRatioToRow(setting, modelName, inputPrice, outputPrice, cachePrice, cacheCreationPrice, groupRatio)
}
