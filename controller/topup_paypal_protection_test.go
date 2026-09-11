package controller

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/require"
)

func TestPayPalCaptureProtectionSurvivesSubscriptionPayload(t *testing.T) {
	payload := `{"id":"CAPTURE-42","custom_id":"ORDER-42","amount":{"value":"10.00","currency_code":"USD"},"seller_protection":{"status":"ELIGIBLE","dispute_categories":["ITEM_NOT_RECEIVED","UNAUTHORIZED_TRANSACTION"]}}`
	var capture payPalCaptureResource
	require.NoError(t, common.UnmarshalJsonStr(payload, &capture))
	require.Equal(t, "ORDER-42", capture.CustomID)
	require.Equal(t, "CAPTURE-42", capture.ID)
	var subscriptionMetadata model.PayPalCaptureMetadata
	require.NoError(t, common.UnmarshalJsonStr(common.GetJsonString(capture), &subscriptionMetadata))
	require.Equal(t, capture.PayPalCaptureMetadata, subscriptionMetadata)
	require.Equal(t, "ELIGIBLE", subscriptionMetadata.SellerProtection.Status)
	require.Len(t, subscriptionMetadata.SellerProtection.DisputeCategories, 2)
}
