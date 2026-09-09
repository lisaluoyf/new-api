package controller

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/require"
)

func TestImageTaskMultipleResultsSurvivePersistence(t *testing.T) {
	data := model.TaskPrivateData{ResultURL: "first", ImageResultURLs: []string{"first", "second"}}
	stored, err := data.Value()
	require.NoError(t, err)
	var restored model.TaskPrivateData
	require.NoError(t, restored.Scan(stored))
	response := buildImageTaskStatusResponse("succeeded", restored.ResultURL, restored.ImageResultURLs...)
	raw, err := common.Marshal(response)
	require.NoError(t, err)
	require.JSONEq(t, `{"data":{"status":"succeeded","result":{"images":[{"url":"first"},{"url":"second"}]}}}`, string(raw))
	legacy, err := common.Marshal(buildImageTaskStatusResponse("succeeded", "first"))
	require.NoError(t, err)
	require.JSONEq(t, `{"data":{"status":"succeeded","result":{"images":[{"url":"first"}]}}}`, string(legacy))
	empty, err := (model.TaskPrivateData{}).Value()
	require.NoError(t, err)
	require.Nil(t, empty)
}
