package controller

import (
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
	"github.com/thanhpk/randstr"
)

type PlategaPayRequest struct {
	Amount int64 `json:"amount"`
	PlanId int   `json:"plan_id,omitempty"`
}

func RequestPlategaAmount(c *gin.Context) {
	if abortIfTopupForbidden(c) {
		return
	}
	var req PlategaPayRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": i18n.T(c, i18n.MsgInvalidParams)})
		return
	}
	id := c.GetInt("id")
	minTopup := getWalletMinTopupForUser(id, setting.PlategaMinTopUp)
	if req.PlanId <= 0 && req.Amount < minTopup {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": fmt.Sprintf("Top-up amount cannot be less than %d", minTopup)})
		return
	}

	group, err := model.GetUserGroup(id, true)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "Failed to get user group"})
		return
	}

	rubAmount := getPlategaPayRubAmount(req.Amount, group, id)
	if rubAmount <= 0.01 {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "Top-up amount is too low"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "success",
		"data": gin.H{
			"rub_amount": fmt.Sprintf("%.2f", rubAmount),
			"usd_amount": req.Amount,
			"usd_to_rub": setting.PlategaUSDRate,
		},
	})
}

func getPlategaPayRubAmount(amount int64, group string, userId int) float64 {
	dAmount := decimal.NewFromInt(amount)
	if operation_setting.GetQuotaDisplayType() == operation_setting.QuotaDisplayTypeTokens {
		dAmount = dAmount.Div(decimal.NewFromFloat(common.QuotaPerUnit))
	}

	topupGroupRatio := common.GetTopupGroupRatio(group)
	if topupGroupRatio == 0 {
		topupGroupRatio = 1
	}

	discount := operation_setting.GetActiveAmountDiscount(int(amount), time.Now())

	rate := setting.PlategaUSDRate
	if rate <= 0 {
		rate = 90
	}

	payRub := dAmount.
		Mul(decimal.NewFromFloat(rate)).
		Mul(decimal.NewFromFloat(topupGroupRatio)).
		Mul(decimal.NewFromFloat(discount))

	// Keep SBP consistent with the other card-like payment paths: only an
	// eligible user's first top-up at the configured promo tier gets the
	// first-top-up factor. IsFirstTopupPromoEligible checks the registration
	// window and successful top-up history, so later payments remain full price.
	payRub = payRub.Mul(decimal.NewFromFloat(firstTopupPromoFactor(userId, amount)))

	return payRub.InexactFloat64()
}

func normalizePlategaTopUpAmount(amount int64) int64 {
	if operation_setting.GetQuotaDisplayType() != operation_setting.QuotaDisplayTypeTokens {
		return amount
	}
	return decimal.NewFromInt(amount).Div(decimal.NewFromFloat(common.QuotaPerUnit)).IntPart()
}

func getPlategaReturnURL() string {
	if strings.TrimSpace(setting.PlategaReturnURL) != "" {
		return setting.PlategaReturnURL
	}
	return strings.TrimRight(system_setting.ServerAddress, "/") + "/console/wallet?show_history=true"
}

func getPlategaFailedURL() string {
	if strings.TrimSpace(setting.PlategaFailedURL) != "" {
		return setting.PlategaFailedURL
	}
	return getPlategaReturnURL()
}

func RequestPlategaPay(c *gin.Context) {
	if abortIfTopupForbidden(c) {
		return
	}
	if !isPlategaTopUpEnabled() {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "Platega top-up is not enabled"})
		return
	}

	var req PlategaPayRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": i18n.T(c, i18n.MsgInvalidParams)})
		return
	}
	id := c.GetInt("id")
	minTopup := getWalletMinTopupForUser(id, setting.PlategaMinTopUp)
	if req.PlanId <= 0 && req.Amount < minTopup {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": fmt.Sprintf("Top-up amount cannot be less than %d", minTopup)})
		return
	}

	user, err := model.GetUserById(id, false)
	if err != nil || user == nil {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": i18n.T(c, i18n.MsgUserNotExists)})
		return
	}
	profileCountry := ""
	profileCountry = user.Country
	var plan *model.SubscriptionPlan
	var terms subscriptionOrderTerms
	payRub := 0.0
	if req.PlanId > 0 {
		plan, terms, err = resolvePaidSubscriptionPayment(id, req.PlanId)
		if err != nil {
			common.ApiErrorMsg(c, err.Error())
			return
		}
		if terms.Payable+0.005 < float64(setting.PlategaMinTopUp) {
			common.ApiErrorMsg(c, fmt.Sprintf("SBP minimum payment amount is $%d", setting.PlategaMinTopUp))
			return
		}
		rate := setting.PlategaUSDRate
		if rate <= 0 {
			rate = 90
		}
		payRub = decimal.NewFromFloat(terms.Payable).Mul(decimal.NewFromFloat(rate)).InexactFloat64()
	} else {
		group, groupErr := model.GetUserGroup(id, true)
		if groupErr != nil {
			c.JSON(http.StatusOK, gin.H{"message": "error", "data": "Failed to get user group"})
			return
		}
		payRub = getPlategaPayRubAmount(req.Amount, group, id)
	}
	if payRub <= 0.01 {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "Top-up amount is too low"})
		return
	}

	tradePrefix := "PLATEGA"
	if plan != nil {
		tradePrefix = "SUB-PLATEGA"
	}
	tradeNo := fmt.Sprintf("%s-%d-%d-%s", tradePrefix, id, time.Now().UnixMilli(), randstr.String(6))
	normalizedAmount := normalizePlategaTopUpAmount(req.Amount)

	if plan != nil {
		order := newPaidSubscriptionOrder(id, plan, terms, tradeNo, model.PaymentMethodPlatega, model.PaymentProviderPlatega)
		if err := order.Insert(); err != nil {
			logger.LogError(c.Request.Context(), fmt.Sprintf("Platega 创建订阅订单失败 user_id=%d trade_no=%s plan_id=%d error=%q", id, tradeNo, plan.Id, err.Error()))
			c.JSON(http.StatusOK, gin.H{"message": "error", "data": "Failed to create order"})
			return
		}
	} else {
		paidUSD := float64(normalizedAmount)
		if setting.PlategaUSDRate > 0 {
			paidUSD = payRub / setting.PlategaUSDRate
		}
		topUp := &model.TopUp{
			UserId:              id,
			Amount:              normalizedAmount,
			PaidAmountUSD:       paidUSD,
			PaidAmountUSDSource: "order",
			Money:               payRub,
			TradeNo:             tradeNo,
			PaymentMethod:       model.PaymentMethodPlatega,
			PaymentProvider:     model.PaymentProviderPlatega,
			CreateTime:          common.GetTimestamp(),
			Status:              common.TopUpStatusPending,
		}
		if err := topUp.FillCountryFromIP(c.ClientIP(), profileCountry).Insert(); err != nil {
			logger.LogError(c.Request.Context(), fmt.Sprintf("Platega 创建本地订单失败 user_id=%d trade_no=%s error=%q", id, tradeNo, err.Error()))
			c.JSON(http.StatusOK, gin.H{"message": "error", "data": "Failed to create order"})
			return
		}
	}
	description := "APIMaster.ai balance top-up"
	returnURL := getPlategaReturnURL()
	failedURL := getPlategaFailedURL()
	if plan != nil {
		description = "APIMaster.ai GPT Pass subscription"
		returnURL = subscriptionPaymentURL(plan, "success")
		failedURL = subscriptionPaymentURL(plan, "cancelled")
	}

	createReq := &service.PlategaCreateTransactionRequest{
		PaymentDetails: service.PlategaPaymentDetails{
			Amount:   payRub,
			Currency: "RUB",
		},
		Description: description,
		Return:      returnURL,
		FailedURL:   failedURL,
		Payload:     tradeNo,
	}

	resp, reqJSON, err := service.CreatePlategaTransaction(c.Request.Context(), createReq)
	if err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("Platega 创建支付失败 user_id=%d trade_no=%s error=%q", id, tradeNo, err.Error()))
		if plan != nil {
			_, _ = tryExpireSubscriptionPayment(tradeNo, model.PaymentProviderPlatega)
		} else {
			_ = model.UpdatePendingTopUpStatus(tradeNo, model.PaymentProviderPlatega, common.TopUpStatusFailed)
		}
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "Failed to start payment"})
		return
	}

	respJSON, _ := common.Marshal(resp)
	plategaOrder := &model.PlategaOrder{
		TradeNo:   tradeNo,
		UserId:    id,
		RubAmount: payRub,
		UsdQuotaAmount: func() int64 {
			if plan != nil {
				return 0
			}
			return normalizedAmount
		}(),
		PlategaTransactionId: resp.TransactionId,
		PaymentMethod:        model.PlategaPaymentMethodSBPQR,
		PlategaStatus:        model.PlategaStatusPending,
		Payload:              tradeNo,
		CreateRequestJSON:    string(reqJSON),
		CreateResponseJSON:   string(respJSON),
		CreateTime:           common.GetTimestamp(),
		UpdateTime:           common.GetTimestamp(),
	}
	if err := plategaOrder.Insert(); err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("Platega 保存订单扩展信息失败 trade_no=%s error=%q", tradeNo, err.Error()))
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "Failed to save order"})
		return
	}

	logger.LogInfo(c.Request.Context(), fmt.Sprintf("Platega 支付订单创建成功 user_id=%d trade_no=%s transaction_id=%s plan_id=%d rub=%.2f amount=%d", id, tradeNo, resp.TransactionId, req.PlanId, payRub, normalizedAmount))
	c.JSON(http.StatusOK, gin.H{
		"message": "success",
		"data": gin.H{
			"redirect_url":   resp.Redirect,
			"transaction_id": resp.TransactionId,
			"order_id":       tradeNo,
			"rub_amount":     payRub,
			"status":         resp.Status,
		},
	})
}

func PlategaCallback(c *gin.Context) {
	if !isPlategaWebhookEnabled() {
		c.String(http.StatusForbidden, "webhook disabled")
		return
	}
	if err := service.AuthenticatePlategaCallback(c.Request.Header); err != nil {
		logger.LogWarn(c.Request.Context(), "Platega callback rejected: authentication failed")
		c.String(http.StatusUnauthorized, "unauthorized")
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 64*1024)
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		c.String(http.StatusBadRequest, "invalid body")
		return
	}
	var payload service.PlategaCallbackPayload
	if err := common.Unmarshal(body, &payload); err != nil {
		c.String(http.StatusBadRequest, "invalid JSON")
		return
	}
	id, err := service.ResolvePlategaID(payload.ID, payload.TransactionId)
	if err != nil {
		c.String(http.StatusBadRequest, "invalid transaction identity")
		return
	}
	order := model.GetPlategaOrderByTransactionId(id)
	if order == nil {
		c.String(http.StatusBadRequest, "unknown transaction")
		return
	}
	if err := service.ValidatePlategaCallbackOrder(&payload, order); err != nil {
		c.String(http.StatusBadRequest, "invalid payment identity or money")
		return
	}
	payload.ID = id
	payload.TransactionId = ""
	canonical, err := common.Marshal(payload)
	if err != nil {
		c.String(http.StatusInternalServerError, "serialization failed")
		return
	}
	event, err := model.SavePlategaEvent(order.TradeNo, id, "callback", c.ClientIP(), string(canonical), payload.Status, 0)
	if err != nil {
		c.String(http.StatusServiceUnavailable, "event persistence failed")
		return
	}
	if err := processPlategaEvent(c.Request.Context(), event); err != nil {
		logger.LogWarn(c.Request.Context(), fmt.Sprintf("Platega event %d retained for retry", event.Id))
		c.String(http.StatusServiceUnavailable, "retry scheduled")
		return
	}
	c.String(http.StatusOK, "OK")
}

func AdminListPlategaOrders(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("p", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}
	orders, total, err := model.ListPlategaOrders(pageSize, (page-1)*pageSize)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	// Legacy rows may contain secret headers. Never expose these through admin UI.
	for _, order := range orders {
		order.CallbackHeadersJSON = `{"legacy_headers":"redacted"}`
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"items": orders, "total": total, "page": page, "page_size": pageSize}})
}

type plategaAdminActionRequest struct {
	TradeNo       string `json:"trade_no"`
	TransactionId string `json:"transaction_id"`
}

func AdminQueryPlategaStatus(c *gin.Context) {
	var req plategaAdminActionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "invalid params"})
		return
	}
	order := model.GetPlategaOrderByTradeNo(req.TradeNo)
	if order == nil {
		order = model.GetPlategaOrderByTransactionId(req.TransactionId)
	}
	if order == nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "order not found"})
		return
	}
	if (req.TradeNo != "" && req.TradeNo != order.TradeNo) || (req.TransactionId != "" && req.TransactionId != order.PlategaTransactionId) {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "order identity conflict"})
		return
	}
	status, err := service.GetPlategaTransactionStatus(c.Request.Context(), order.PlategaTransactionId)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": err.Error()})
		return
	}
	if err := service.ValidatePlategaAPIOrder(status, order); err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"success": false, "message": "API payment verification failed"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": status})
}

func AdminRetryPlategaCallback(c *gin.Context) {
	var req plategaAdminActionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "invalid params"})
		return
	}
	order := model.GetPlategaOrderByTradeNo(req.TradeNo)
	if order == nil && req.TradeNo == "" {
		order = model.GetPlategaOrderByTransactionId(req.TransactionId)
	}
	if order == nil || (req.TransactionId != "" && req.TransactionId != order.PlategaTransactionId) {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "order identity conflict"})
		return
	}
	reconcileAdminPlategaOrder(c, order)
}

func reconcileAdminPlategaOrder(c *gin.Context, order *model.PlategaOrder) {
	// Admin reconciliation is an authenticated API query, never a fabricated
	// callback populated with locally guessed amount/currency/status.
	payload, _ := common.Marshal(map[string]any{"trade_no": order.TradeNo, "transaction_id": order.PlategaTransactionId, "requested_at": common.GetTimestamp()})
	event, err := model.SavePlategaEvent(order.TradeNo, order.PlategaTransactionId, "admin-query", c.ClientIP(), string(payload), "", c.GetInt("id"))
	if err == nil {
		err = processPlategaEvent(c.Request.Context(), event)
	}
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"success": false, "message": "verification failed; event retained for retry"})
		return
	}
	var saved model.PlategaEvent
	if err := model.DB.First(&saved, event.Id).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": saved.Status == "done" && model.NormalizePlategaAPIStatus(saved.APIStatus) == model.PlategaStatusConfirmed, "message": saved.Status, "data": saved})
}
