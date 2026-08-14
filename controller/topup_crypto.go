package controller

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"math/big"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"

	ethaccounts "github.com/ethereum/go-ethereum/accounts"
	"github.com/ethereum/go-ethereum/common/hexutil"
	ethcrypto "github.com/ethereum/go-ethereum/crypto"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type cryptoChainConfig struct {
	rpcEnvKey      string
	defaultRPCs    []string
	usdtAddress    string
	usdcAddress    string
	usdtDecimals   int
	usdcDecimals   int
	nativeCGID     string
	nativeDecimals int
}

const transferEventTopic = "0xddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef"

const (
	cryptoIntentPurposeWalletTopup     = "wallet_topup"
	cryptoIntentPurposeGPTSubscription = "gpt_subscription"
)

var cryptoChains = map[string]cryptoChainConfig{
	"eth": {
		rpcEnvKey:      "CRYPTO_RPC_ETH",
		defaultRPCs:    []string{"https://ethereum-rpc.publicnode.com", "https://eth.api.onfinality.io/public", "https://eth.drpc.org", "https://eth.blockrazor.xyz"},
		usdtAddress:    "0xdAC17F958D2ee523a2206206994597C13D831ec7",
		usdcAddress:    "0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48",
		usdtDecimals:   6,
		usdcDecimals:   6,
		nativeCGID:     "ethereum",
		nativeDecimals: 18,
	},
	"bsc": {
		rpcEnvKey:      "CRYPTO_RPC_BSC",
		defaultRPCs:    []string{"https://bsc-dataseed.binance.org"},
		usdtAddress:    "0x55d398326f99059fF775485246999027B3197955",
		usdcAddress:    "0x8AC76a51cc950d9822D68b83fE1Ad97B32Cd580d",
		usdtDecimals:   18,
		usdcDecimals:   18,
		nativeCGID:     "binancecoin",
		nativeDecimals: 18,
	},
	"polygon": {
		rpcEnvKey:      "CRYPTO_RPC_POLYGON",
		defaultRPCs:    []string{"https://polygon-bor-rpc.publicnode.com", "https://polygon.drpc.org"},
		usdtAddress:    "0xc2132D05D31c914a87C6611C10748AEb04B58e8F",
		usdcAddress:    "0x2791Bca1f2de4661ED88A30C99A7a9449Aa84174",
		usdtDecimals:   6,
		usdcDecimals:   6,
		nativeCGID:     "polygon-ecosystem-token",
		nativeDecimals: 18,
	},
	"arbitrum": {
		rpcEnvKey:      "CRYPTO_RPC_ARBITRUM",
		defaultRPCs:    []string{"https://arb1.arbitrum.io/rpc"},
		usdtAddress:    "0xFd086bC7CD5C481DCC9C85ebE478A1C0b69FCbb9",
		usdcAddress:    "0xaf88d065e77c8cC2239327C5EDb3A432268e5831",
		usdtDecimals:   6,
		usdcDecimals:   6,
		nativeCGID:     "ethereum",
		nativeDecimals: 18,
	},
	"base": {
		rpcEnvKey:      "CRYPTO_RPC_BASE",
		defaultRPCs:    []string{"https://mainnet.base.org"},
		usdtAddress:    "",
		usdcAddress:    "0x833589fCD6eDb6E08f4c7C32D4f71b54bdA02913",
		usdtDecimals:   0,
		usdcDecimals:   6,
		nativeCGID:     "ethereum",
		nativeDecimals: 18,
	},
}

var (
	evmAddressPattern = regexp.MustCompile(`^0x[0-9a-fA-F]{40}$`)
	txHashPattern     = regexp.MustCompile(`^0x[0-9a-fA-F]{64}$`)
)

func cryptoShortValue(value string) string {
	value = strings.TrimSpace(value)
	if len(value) <= 18 {
		return value
	}
	return value[:10] + "..." + value[len(value)-6:]
}

func recordCryptoIntentLog(userId int, content string, intent *model.CryptoDepositIntent, extra map[string]interface{}) {
	if userId <= 0 || strings.TrimSpace(content) == "" {
		return
	}
	adminInfo := map[string]interface{}{
		"payment_method":   "crypto",
		"payment_provider": "crypto",
	}
	if intent != nil {
		adminInfo["intent_id"] = intent.Id
		adminInfo["chain"] = intent.Chain
		adminInfo["token_symbol"] = intent.TokenSymbol
		adminInfo["wallet_address_from"] = intent.WalletAddressFrom
		adminInfo["expected_to_address"] = intent.ExpectedToAddress
		if intent.TxHash != nil && strings.TrimSpace(*intent.TxHash) != "" {
			adminInfo["tx_hash"] = *intent.TxHash
		}
		if strings.TrimSpace(intent.Status) != "" {
			adminInfo["intent_status"] = intent.Status
		}
		if strings.TrimSpace(intent.ErrorMessage) != "" {
			adminInfo["error_message"] = intent.ErrorMessage
		}
	}
	for key, value := range extra {
		adminInfo[key] = value
	}
	model.RecordLogWithAdminInfo(userId, model.LogTypeTopup, content, adminInfo)
}

func getPlatformWallet() string {
	wallet := os.Getenv("PLATFORM_WALLET_ADDRESS")
	if wallet == "" {
		wallet = "0x33de43dad6955655ec0543f32069ac331e633c9c"
	}
	return strings.ToLower(strings.TrimSpace(wallet))
}

func getRPCs(cfg cryptoChainConfig) []string {
	rpcs := make([]string, 0, len(cfg.defaultRPCs)+1)
	seen := make(map[string]struct{}, len(cfg.defaultRPCs)+1)
	appendRPC := func(value string) {
		value = strings.TrimSpace(value)
		if value == "" {
			return
		}
		if _, ok := seen[value]; ok {
			return
		}
		seen[value] = struct{}{}
		rpcs = append(rpcs, value)
	}
	if value := os.Getenv(cfg.rpcEnvKey); value != "" {
		appendRPC(value)
	}
	for _, rpc := range cfg.defaultRPCs {
		appendRPC(rpc)
	}
	return rpcs
}

type jsonRPCRequest struct {
	JSONRPC string        `json:"jsonrpc"`
	Method  string        `json:"method"`
	Params  []interface{} `json:"params"`
	ID      int           `json:"id"`
}

type jsonRPCResponse struct {
	Result interface{} `json:"result"`
	Error  *struct {
		Message string `json:"message"`
	} `json:"error"`
}

func ethCall(ctx context.Context, rpcURL string, method string, params []interface{}) (interface{}, error) {
	reqBody, _ := json.Marshal(jsonRPCRequest{JSONRPC: "2.0", Method: method, Params: params, ID: 1})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, rpcURL, bytes.NewReader(reqBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var rpcResp jsonRPCResponse
	if err := json.Unmarshal(body, &rpcResp); err != nil {
		return nil, fmt.Errorf("JSON-RPC decode error: %w", err)
	}
	if rpcResp.Error != nil {
		return nil, fmt.Errorf("JSON-RPC error: %s", rpcResp.Error.Message)
	}
	return rpcResp.Result, nil
}

func waitForReceipt(ctx context.Context, rpcURL, txHash string) (map[string]interface{}, error) {
	deadline := time.Now().Add(120 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
		result, err := ethCall(ctx, rpcURL, "eth_getTransactionReceipt", []interface{}{txHash})
		if err == nil && result != nil {
			if receipt, ok := result.(map[string]interface{}); ok {
				return receipt, nil
			}
		}
		time.Sleep(5 * time.Second)
	}
	return nil, fmt.Errorf("timed out waiting for receipt")
}

func hexToDecimal(hexStr string) (*big.Int, bool) {
	trimmed := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(hexStr)), "0x")
	n := new(big.Int)
	_, ok := n.SetString(trimmed, 16)
	return n, ok
}

func isValidEVMAddress(value string) bool {
	return evmAddressPattern.MatchString(strings.TrimSpace(value))
}

func isValidTxHash(value string) bool {
	return txHashPattern.MatchString(strings.TrimSpace(value))
}

func normalizeTopicAddress(topic string) string {
	trimmed := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(topic)), "0x")
	if len(trimmed) < 40 {
		return ""
	}
	return "0x" + trimmed[len(trimmed)-40:]
}

func getNativeSymbol(chain string) string {
	switch strings.ToLower(strings.TrimSpace(chain)) {
	case "eth":
		return "ETH"
	case "bsc":
		return "BNB"
	case "polygon":
		return "POL"
	case "arbitrum", "base":
		return "ETH"
	default:
		return ""
	}
}

func getExpectedTokenAddress(cfg cryptoChainConfig, tokenSymbol string) (string, bool) {
	switch strings.ToUpper(strings.TrimSpace(tokenSymbol)) {
	case "USDT":
		if cfg.usdtAddress == "" {
			return "", false
		}
		return strings.ToLower(cfg.usdtAddress), true
	case "USDC":
		if cfg.usdcAddress == "" {
			return "", false
		}
		return strings.ToLower(cfg.usdcAddress), true
	default:
		return "", false
	}
}

func isSupportedCryptoToken(chain string, cfg cryptoChainConfig, tokenSymbol string) bool {
	normalized := strings.ToUpper(strings.TrimSpace(tokenSymbol))
	if normalized == "" {
		return false
	}
	if normalized == getNativeSymbol(chain) {
		return true
	}
	_, ok := getExpectedTokenAddress(cfg, normalized)
	return ok
}

func getTokenDecimals(cfg cryptoChainConfig, tokenSymbol string) int {
	switch strings.ToUpper(strings.TrimSpace(tokenSymbol)) {
	case "USDT":
		return cfg.usdtDecimals
	case "USDC":
		return cfg.usdcDecimals
	default:
		return 0
	}
}

func isDuplicateDBError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "duplicate entry") ||
		strings.Contains(msg, "duplicate key") ||
		strings.Contains(msg, "unique constraint failed")
}

func fetchCoinPrice(ctx context.Context, coingeckoID string) (float64, error) {
	url := fmt.Sprintf("https://api.coingecko.com/api/v3/simple/price?ids=%s&vs_currencies=usd", coingeckoID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, err
	}
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	var result map[string]map[string]float64
	if err := json.Unmarshal(body, &result); err != nil {
		return 0, err
	}
	price, ok := result[coingeckoID]["usd"]
	if !ok || price <= 0 {
		return 0, fmt.Errorf("price not found for %s", coingeckoID)
	}
	return price, nil
}

func buildCryptoIntentChallenge(intent *model.CryptoDepositIntent) string {
	return fmt.Sprintf(
		"APIMaster Crypto Deposit Authorization\nIntent ID: %s\nPurpose: %s\nExpected USD: %.6f\nChain: %s\nToken: %s\nWallet: %s\nRecipient: %s\nExpires At: %d",
		intent.Id,
		intent.Purpose,
		intent.ExpectedUsdAmount,
		strings.ToUpper(intent.Chain),
		intent.TokenSymbol,
		intent.WalletAddressFrom,
		intent.ExpectedToAddress,
		intent.ExpiresAt,
	)
}

func verifyWalletSignature(intent *model.CryptoDepositIntent, signature string) error {
	decoded, err := hexutil.Decode(strings.TrimSpace(signature))
	if err != nil {
		return fmt.Errorf("invalid signature encoding")
	}
	if len(decoded) != 65 {
		return fmt.Errorf("invalid signature length")
	}
	if decoded[64] >= 27 {
		decoded[64] -= 27
	}
	if decoded[64] > 1 {
		return fmt.Errorf("invalid signature recovery id")
	}

	hash := ethaccounts.TextHash([]byte(intent.Challenge))
	pubKey, err := ethcrypto.SigToPub(hash, decoded)
	if err != nil {
		return fmt.Errorf("failed to recover signature")
	}
	recovered := strings.ToLower(ethcrypto.PubkeyToAddress(*pubKey).Hex())
	if recovered != intent.WalletAddressFrom {
		return fmt.Errorf("signature does not match wallet address")
	}
	return nil
}

func verifyIntentOnChain(intent *model.CryptoDepositIntent, cfg cryptoChainConfig, rpcURLs []string) (float64, error) {
	if len(rpcURLs) == 0 {
		return 0, fmt.Errorf("no RPC configured")
	}

	var (
		receipt   map[string]interface{}
		txMap     map[string]interface{}
		lastErr   error
		rpcURL    string
		usedIndex int
	)

	for i, candidate := range rpcURLs {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		currentReceipt, err := waitForReceipt(ctx, candidate, *intent.TxHash)
		cancel()
		if err != nil || currentReceipt == nil {
			lastErr = err
			common.SysLog(fmt.Sprintf("crypto: receipt error txHash=%s rpc=%s err=%v", *intent.TxHash, candidate, err))
			continue
		}

		ctx, cancel = context.WithTimeout(context.Background(), 30*time.Second)
		txResult, err := ethCall(ctx, candidate, "eth_getTransactionByHash", []interface{}{*intent.TxHash})
		cancel()
		if err != nil || txResult == nil {
			lastErr = err
			common.SysLog(fmt.Sprintf("crypto: tx lookup error txHash=%s rpc=%s err=%v", *intent.TxHash, candidate, err))
			continue
		}

		currentTxMap, ok := txResult.(map[string]interface{})
		if !ok {
			lastErr = fmt.Errorf("unexpected tx response type")
			continue
		}

		receipt = currentReceipt
		txMap = currentTxMap
		rpcURL = candidate
		usedIndex = i
		break
	}

	if receipt == nil || txMap == nil {
		if lastErr == nil {
			lastErr = fmt.Errorf("transaction receipt unavailable")
		}
		return 0, lastErr
	}
	if usedIndex > 0 {
		common.SysLog(fmt.Sprintf("crypto: rpc fallback hit txHash=%s rpc=%s", *intent.TxHash, rpcURL))
	}

	statusHex, _ := receipt["status"].(string)
	if statusHex != "0x1" {
		return 0, fmt.Errorf("transaction failed on-chain")
	}

	fromField, _ := txMap["from"].(string)
	if strings.ToLower(strings.TrimSpace(fromField)) != intent.WalletAddressFrom {
		return 0, fmt.Errorf("wallet address mismatch")
	}

	platformWallet := strings.ToLower(strings.TrimSpace(intent.ExpectedToAddress))
	if platformWallet == "" {
		platformWallet = getPlatformWallet()
	}
	if intent.TokenAddress == "" {
		toField, _ := txMap["to"].(string)
		valueField, _ := txMap["value"].(string)
		if strings.ToLower(strings.TrimSpace(toField)) != platformWallet {
			return 0, fmt.Errorf("recipient mismatch")
		}
		weiAmount, ok := hexToDecimal(valueField)
		if !ok || weiAmount.Sign() <= 0 {
			return 0, fmt.Errorf("invalid transfer value")
		}
		nativeAmt := decimal.NewFromBigInt(weiAmount, int32(-cfg.nativeDecimals))
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		price, err := fetchCoinPrice(ctx, cfg.nativeCGID)
		cancel()
		if err != nil || price <= 0 {
			return 0, fmt.Errorf("price lookup failed: %w", err)
		}
		nativeFloat, _ := nativeAmt.Float64()
		return nativeFloat * price, nil
	}

	toField, _ := txMap["to"].(string)
	if strings.ToLower(strings.TrimSpace(toField)) != intent.TokenAddress {
		return 0, fmt.Errorf("token contract mismatch")
	}

	logsRaw, _ := receipt["logs"].([]interface{})
	for _, logRaw := range logsRaw {
		logMap, ok := logRaw.(map[string]interface{})
		if !ok {
			continue
		}
		logAddr, _ := logMap["address"].(string)
		if strings.ToLower(strings.TrimSpace(logAddr)) != intent.TokenAddress {
			continue
		}
		topics, _ := logMap["topics"].([]interface{})
		if len(topics) < 3 {
			continue
		}
		topic0, _ := topics[0].(string)
		if strings.ToLower(strings.TrimSpace(topic0)) != transferEventTopic {
			continue
		}
		topic1, _ := topics[1].(string)
		topic2, _ := topics[2].(string)
		if normalizeTopicAddress(topic1) != intent.WalletAddressFrom {
			continue
		}
		if normalizeTopicAddress(topic2) != platformWallet {
			continue
		}

		data, _ := logMap["data"].(string)
		amountBig, ok := hexToDecimal(data)
		if !ok {
			continue
		}
		decimals := getTokenDecimals(cfg, intent.TokenSymbol)
		if decimals <= 0 {
			return 0, fmt.Errorf("token decimals unavailable")
		}
		usdAmount, _ := decimal.NewFromBigInt(amountBig, int32(-decimals)).Float64()
		if usdAmount <= 0 {
			continue
		}
		return usdAmount, nil
	}

	return 0, fmt.Errorf("matching token transfer not found")
}

func markCryptoIntentFailed(intentId string, err error) {
	message := ""
	if err != nil {
		message = err.Error()
	}
	updates := map[string]interface{}{
		"status":        model.CryptoDepositIntentStatusFailed,
		"error_message": message,
	}
	result := model.DB.Model(&model.CryptoDepositIntent{}).
		Where("id = ? AND status = ?", intentId, model.CryptoDepositIntentStatusPending).
		Updates(updates)
	if result.Error != nil {
		updateErr := result.Error
		common.SysLog(fmt.Sprintf("crypto: mark failed intent=%s err=%v", intentId, updateErr))
		return
	}
	if result.RowsAffected == 0 {
		return
	}

	var intent model.CryptoDepositIntent
	if loadErr := model.DB.Where("id = ?", intentId).First(&intent).Error; loadErr != nil {
		common.SysLog(fmt.Sprintf("crypto: reload failed intent=%s err=%v", intentId, loadErr))
		return
	}
	if intent.Purpose == cryptoIntentPurposeGPTSubscription && strings.TrimSpace(intent.SubscriptionOrderTradeNo) != "" {
		if _, expireErr := tryExpireSubscriptionPayment(intent.SubscriptionOrderTradeNo, model.PaymentProviderCrypto); expireErr != nil {
			common.SysLog(fmt.Sprintf("crypto: expire subscription order failed intent=%s tradeNo=%s err=%v", intent.Id, intent.SubscriptionOrderTradeNo, expireErr))
		}
	}
	txHash := ""
	if intent.TxHash != nil {
		txHash = cryptoShortValue(*intent.TxHash)
	}
	recordCryptoIntentLog(
		intent.UserId,
		fmt.Sprintf(
			"加密货币充值失败：%s %s，交易 %s，原因：%s",
			strings.ToUpper(intent.Chain),
			intent.TokenSymbol,
			txHash,
			message,
		),
		&intent,
		map[string]interface{}{"stage": "verify_failed"},
	)
}

func matchCryptoAmountDiscountTier(usdValue float64) (int, float64, bool) {
	if usdValue <= 0 {
		return 0, 0, false
	}

	bestTier := 0
	bestDiscount := 0.0
	for amount, discount := range operation_setting.GetPaymentSetting().AmountDiscount {
		if amount <= 0 || discount <= 0 || discount >= 1 {
			continue
		}
		threshold := float64(amount) * discount
		if usdValue+1e-9 < threshold {
			continue
		}
		if amount > bestTier {
			bestTier = amount
			bestDiscount = discount
		}
	}

	if bestTier == 0 {
		return 0, 0, false
	}
	return bestTier, bestDiscount, true
}

func cryptoFirstTopupPromoMinPaidUSD() float64 {
	threshold := float64(common.FirstTopupPromoAmount) * common.FirstTopupPromoDiscount
	if threshold <= 0 {
		return 0
	}
	// Crypto users usually pay round-dollar amounts; floor the nominal threshold
	// so "$7.5 gets $10" becomes "starting from $7, apply the same uplift rule".
	floored := math.Floor(threshold + 1e-9)
	if floored >= 1 {
		return floored
	}
	return threshold
}

func applyCryptoFirstTopupPromo(usdValue float64) (float64, float64, bool) {
	if usdValue <= 0 || !common.FirstTopupPromoEnabled {
		return usdValue, 0, false
	}
	if common.FirstTopupPromoDiscount <= 0 || common.FirstTopupPromoDiscount >= 1 {
		return usdValue, 0, false
	}
	if minPaid := cryptoFirstTopupPromoMinPaidUSD(); minPaid > 0 && usdValue+1e-9 < minPaid {
		return usdValue, 0, false
	}

	bonusBase := usdValue
	if capUsd := float64(common.FirstTopupPromoAmount); bonusBase > capUsd {
		bonusBase = capUsd
	}
	bonus := bonusBase * (1/common.FirstTopupPromoDiscount - 1)
	return usdValue + bonus, bonus, true
}

func verifyAndCredit(intentId string) {
	var intent model.CryptoDepositIntent
	if err := model.DB.Where("id = ?", intentId).First(&intent).Error; err != nil {
		common.SysLog(fmt.Sprintf("crypto: load intent failed intent=%s err=%v", intentId, err))
		return
	}
	if intent.Status != model.CryptoDepositIntentStatusPending || intent.TxHash == nil {
		return
	}

	cfg, ok := cryptoChains[intent.Chain]
	if !ok {
		markCryptoIntentFailed(intentId, fmt.Errorf("unsupported chain"))
		return
	}

	usdValue, err := verifyIntentOnChain(&intent, cfg, getRPCs(cfg))
	if err != nil {
		common.SysLog(fmt.Sprintf("crypto: verify failed intent=%s txHash=%s err=%v", intent.Id, *intent.TxHash, err))
		markCryptoIntentFailed(intentId, err)
		return
	}

	if intent.Purpose == cryptoIntentPurposeGPTSubscription {
		if strings.TrimSpace(intent.SubscriptionOrderTradeNo) == "" || intent.ExpectedUsdAmount <= 0 {
			markCryptoIntentFailed(intentId, fmt.Errorf("subscription payment metadata missing"))
			return
		}
		if usdValue+0.005 < intent.ExpectedUsdAmount {
			markCryptoIntentFailed(intentId, fmt.Errorf("payment amount too low: expected %.2f USD, received %.4f USD", intent.ExpectedUsdAmount, usdValue))
			return
		}

		tradeNo := intent.SubscriptionOrderTradeNo
		LockOrder(tradeNo)
		handled, completeErr := tryCompleteSubscriptionPayment(
			tradeNo,
			fmt.Sprintf("crypto intent=%s tx_hash=%s usd=%.6f", intent.Id, *intent.TxHash, usdValue),
			model.PaymentProviderCrypto,
			model.PaymentMethodCrypto,
		)
		UnlockOrder(tradeNo)
		if completeErr != nil || !handled {
			if completeErr == nil {
				completeErr = fmt.Errorf("subscription order not found")
			}
			common.SysLog(fmt.Sprintf("crypto: subscription completion failed intent=%s tradeNo=%s err=%v", intent.Id, tradeNo, completeErr))
			markCryptoIntentFailed(intentId, completeErr)
			return
		}

		now := common.GetTimestamp()
		result := model.DB.Model(&model.CryptoDepositIntent{}).
			Where("id = ? AND status = ?", intent.Id, model.CryptoDepositIntentStatusPending).
			Updates(map[string]interface{}{
				"status":        model.CryptoDepositIntentStatusConfirmed,
				"usd_added":     usdValue,
				"confirmed_at":  now,
				"error_message": "",
			})
		if result.Error != nil {
			common.SysLog(fmt.Sprintf("crypto: subscription intent confirmation update failed intent=%s err=%v", intent.Id, result.Error))
			return
		}
		recordCryptoIntentLog(
			intent.UserId,
			fmt.Sprintf("使用加密货币购买 GPT 订阅成功，支付金额：%.4f USD", usdValue),
			&intent,
			map[string]interface{}{
				"stage":                       "subscription_confirmed",
				"subscription_order_trade_no": tradeNo,
			},
		)
		common.SysLog(fmt.Sprintf("crypto: subscription confirmed userId=%d intent=%s tradeNo=%s txHash=%s usd=%.4f", intent.UserId, intent.Id, tradeNo, *intent.TxHash, usdValue))
		return
	}

	creditUsd := usdValue
	if matchedTier, matchedDiscount, ok := matchCryptoAmountDiscountTier(usdValue); ok {
		creditUsd = usdValue / matchedDiscount
		common.SysLog(fmt.Sprintf("crypto: amount-discount promo userId=%d tier=%d paid=%.4f credit=%.4f", intent.UserId, matchedTier, usdValue, creditUsd))
	} else if eligible, _ := model.IsFirstTopupPromoEligible(intent.UserId); eligible {
		if promoCredit, bonus, applied := applyCryptoFirstTopupPromo(usdValue); applied {
			creditUsd = promoCredit
			common.SysLog(fmt.Sprintf("crypto: first-topup promo userId=%d paid=%.4f bonus=%.4f credit=%.4f", intent.UserId, usdValue, bonus, creditUsd))
		}
	}
	quotaToAdd := int(decimal.NewFromFloat(creditUsd).Mul(decimal.NewFromFloat(common.QuotaPerUnit)).IntPart())
	tradeNo := fmt.Sprintf("CRYPTO:%s:%s", strings.ToUpper(intent.Chain), *intent.TxHash)
	now := common.GetTimestamp()

	createdNewTopup := false
	err = model.DB.Transaction(func(tx *gorm.DB) error {
		var current model.CryptoDepositIntent
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", intentId).First(&current).Error; err != nil {
			return err
		}
		if current.Status == model.CryptoDepositIntentStatusConfirmed {
			return nil
		}
		if current.Status != model.CryptoDepositIntentStatusPending {
			return fmt.Errorf("intent status is %s", current.Status)
		}
		if current.TxHash == nil || *current.TxHash != *intent.TxHash {
			return fmt.Errorf("intent transaction hash mismatch")
		}

		var existingTopup model.TopUp
		err := tx.Where("trade_no = ?", tradeNo).First(&existingTopup).Error
		if err == nil {
			return tx.Model(&current).Updates(map[string]interface{}{
				"status":        model.CryptoDepositIntentStatusConfirmed,
				"usd_added":     creditUsd,
				"confirmed_at":  now,
				"error_message": "",
			}).Error
		}
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}

		topUp := &model.TopUp{
			UserId:          current.UserId,
			Amount:          int64(math.Round(creditUsd)),
			CreditedAmount:  creditUsd,
			Money:           usdValue,
			TradeNo:         tradeNo,
			PaymentMethod:   "crypto",
			PaymentProvider: "crypto",
			CreateTime:      now,
			CompleteTime:    now,
			Status:          common.TopUpStatusSuccess,
		}
		if err := tx.Create(topUp).Error; err != nil {
			return err
		}
		if err := tx.Model(&model.User{}).Where("id = ?", current.UserId).Update("quota", gorm.Expr("quota + ?", quotaToAdd)).Error; err != nil {
			return err
		}
		if err := tx.Model(&current).Updates(map[string]interface{}{
			"status":        model.CryptoDepositIntentStatusConfirmed,
			"usd_added":     creditUsd,
			"confirmed_at":  now,
			"error_message": "",
		}).Error; err != nil {
			return err
		}
		createdNewTopup = true
		return nil
	})
	if err != nil {
		common.SysLog(fmt.Sprintf("crypto: credit failed intent=%s txHash=%s err=%v", intent.Id, *intent.TxHash, err))
		markCryptoIntentFailed(intentId, err)
		return
	}

	if !createdNewTopup {
		return
	}

	if err := model.IncrUserQuotaCache(intent.UserId, int64(quotaToAdd)); err != nil {
		common.SysLog(fmt.Sprintf("crypto: update quota cache failed userId=%d intent=%s err=%v", intent.UserId, intent.Id, err))
	}
	model.RecordTopupLog(intent.UserId, fmt.Sprintf("使用加密货币充值成功，充值金额: %v，支付金额：%.2f", logger.FormatQuota(quotaToAdd), usdValue), "", "crypto", "crypto")
	model.OnTopupSucceeded(intent.UserId, quotaToAdd, "crypto", tradeNo)
	common.SysLog(fmt.Sprintf("crypto: confirmed userId=%d intent=%s txHash=%s usd=%.4f quota=%d", intent.UserId, intent.Id, *intent.TxHash, usdValue, quotaToAdd))
}

type createCryptoIntentRequest struct {
	Chain             string `json:"chain"`
	TokenSymbol       string `json:"token_symbol"`
	WalletAddressFrom string `json:"wallet_address_from"`
	PlanId            int    `json:"plan_id,omitempty"`
}

type submitCryptoRequest struct {
	IntentId        string `json:"intent_id"`
	TxHash          string `json:"tx_hash"`
	WalletSignature string `json:"wallet_signature"`
}

func CreateCryptoDepositIntent(c *gin.Context) {
	if abortIfTopupForbidden(c) {
		return
	}
	userId := c.GetInt("id")
	if userId == 0 {
		c.JSON(http.StatusUnauthorized, gin.H{"success": false, "error": "unauthorized"})
		return
	}
	TouchUserCountry(userId, c.ClientIP())

	var req createCryptoIntentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "invalid request"})
		return
	}

	chain := strings.ToLower(strings.TrimSpace(req.Chain))
	cfg, ok := cryptoChains[chain]
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "unsupported chain"})
		return
	}

	tokenSymbol := strings.ToUpper(strings.TrimSpace(req.TokenSymbol))
	if !isSupportedCryptoToken(chain, cfg, tokenSymbol) {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "unsupported token"})
		return
	}

	walletAddress := strings.ToLower(strings.TrimSpace(req.WalletAddressFrom))
	if !isValidEVMAddress(walletAddress) {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "invalid wallet address"})
		return
	}

	var plan *model.SubscriptionPlan
	var terms subscriptionOrderTerms
	if req.PlanId > 0 {
		var err error
		plan, terms, err = resolveGPTSubscriptionPayment(userId, req.PlanId)
		if err != nil {
			common.ApiErrorMsg(c, err.Error())
			return
		}
	}

	tokenAddress, _ := getExpectedTokenAddress(cfg, tokenSymbol)
	intent := &model.CryptoDepositIntent{
		Id:                common.GetUUID(),
		UserId:            userId,
		Chain:             chain,
		TokenSymbol:       tokenSymbol,
		TokenAddress:      tokenAddress,
		WalletAddressFrom: walletAddress,
		ExpectedToAddress: getPlatformWallet(),
		Purpose:           cryptoIntentPurposeWalletTopup,
		ExpiresAt:         common.GetTimestamp() + 30*60,
	}
	if plan != nil {
		intent.Purpose = cryptoIntentPurposeGPTSubscription
		intent.ExpectedUsdAmount = terms.Payable
		intent.SubscriptionOrderTradeNo = fmt.Sprintf("CRYPTO-SUB:%s", intent.Id)
	}
	intent.Challenge = buildCryptoIntentChallenge(intent)

	if err := model.DB.Transaction(func(tx *gorm.DB) error {
		if plan != nil {
			order := newGPTSubscriptionOrder(
				userId,
				plan,
				terms,
				intent.SubscriptionOrderTradeNo,
				model.PaymentMethodCrypto,
				model.PaymentProviderCrypto,
			)
			if err := tx.Create(order).Error; err != nil {
				return err
			}
		}
		return tx.Create(intent).Error
	}); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": "failed to create deposit intent"})
		return
	}
	recordCryptoIntentLog(
		userId,
		fmt.Sprintf(
			"发起加密货币充值：%s %s，钱包 %s，等待转账",
			strings.ToUpper(intent.Chain),
			intent.TokenSymbol,
			cryptoShortValue(intent.WalletAddressFrom),
		),
		intent,
		map[string]interface{}{"stage": "intent_created"},
	)

	c.JSON(http.StatusOK, gin.H{
		"success":           true,
		"depositId":         intent.Id,
		"challenge":         intent.Challenge,
		"expiresAt":         intent.ExpiresAt,
		"toAddress":         intent.ExpectedToAddress,
		"chain":             intent.Chain,
		"token":             intent.TokenSymbol,
		"purpose":           intent.Purpose,
		"expectedUsdAmount": intent.ExpectedUsdAmount,
	})
}

func SubmitCryptoDeposit(c *gin.Context) {
	if abortIfTopupForbidden(c) {
		return
	}
	userId := c.GetInt("id")
	if userId == 0 {
		c.JSON(http.StatusUnauthorized, gin.H{"success": false, "error": "unauthorized"})
		return
	}
	TouchUserCountry(userId, c.ClientIP())

	var req submitCryptoRequest
	if err := c.ShouldBindJSON(&req); err != nil || strings.TrimSpace(req.IntentId) == "" || strings.TrimSpace(req.TxHash) == "" || strings.TrimSpace(req.WalletSignature) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "invalid request"})
		return
	}

	txHash := strings.ToLower(strings.TrimSpace(req.TxHash))
	if !isValidTxHash(txHash) {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "invalid transaction hash"})
		return
	}

	var intent model.CryptoDepositIntent
	if err := model.DB.Where("id = ? AND user_id = ?", strings.TrimSpace(req.IntentId), userId).First(&intent).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"success": false, "error": "deposit intent not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": "failed to load deposit intent"})
		return
	}

	if intent.Status == model.CryptoDepositIntentStatusConfirmed {
		c.JSON(http.StatusOK, gin.H{"success": true, "depositId": intent.Id})
		return
	}
	if intent.ExpiresAt < common.GetTimestamp() {
		markCryptoIntentFailed(intent.Id, fmt.Errorf("deposit intent expired"))
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "deposit intent expired"})
		return
	}
	if err := verifyWalletSignature(&intent, req.WalletSignature); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": err.Error()})
		return
	}

	submittedNewTxHash := false
	err := model.DB.Transaction(func(tx *gorm.DB) error {
		var current model.CryptoDepositIntent
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ? AND user_id = ?", intent.Id, userId).
			First(&current).Error; err != nil {
			return err
		}
		if current.Status == model.CryptoDepositIntentStatusConfirmed {
			return nil
		}
		if current.Status != model.CryptoDepositIntentStatusPending {
			return fmt.Errorf("deposit intent is not pending")
		}
		if current.ExpiresAt < common.GetTimestamp() {
			return fmt.Errorf("deposit intent expired")
		}
		if current.TxHash != nil {
			if *current.TxHash != txHash {
				return fmt.Errorf("deposit intent already submitted with another transaction")
			}
			return nil
		}
		current.TxHash = &txHash
		current.WalletSignature = strings.TrimSpace(req.WalletSignature)
		current.VerifiedAt = common.GetTimestamp()
		submittedNewTxHash = true
		return tx.Save(&current).Error
	})
	if err != nil {
		if isDuplicateDBError(err) {
			var existing model.CryptoDepositIntent
			lookupErr := model.DB.Where("chain = ? AND tx_hash = ?", intent.Chain, txHash).First(&existing).Error
			if lookupErr == nil && existing.Id == intent.Id {
				c.JSON(http.StatusOK, gin.H{"success": true, "depositId": intent.Id})
				return
			}
			c.JSON(http.StatusConflict, gin.H{"success": false, "error": "transaction already claimed"})
			return
		}
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": err.Error()})
		return
	}
	if submittedNewTxHash {
		intent.TxHash = &txHash
		intent.WalletSignature = strings.TrimSpace(req.WalletSignature)
		intent.VerifiedAt = common.GetTimestamp()
		recordCryptoIntentLog(
			userId,
			fmt.Sprintf(
				"提交加密货币交易：%s %s，交易 %s，等待链上确认",
				strings.ToUpper(intent.Chain),
				intent.TokenSymbol,
				cryptoShortValue(txHash),
			),
			&intent,
			map[string]interface{}{"stage": "tx_submitted"},
		)
	}

	go verifyAndCredit(intent.Id)

	c.JSON(http.StatusOK, gin.H{"success": true, "depositId": intent.Id})
}

func GetCryptoDeposit(c *gin.Context) {
	userId := c.GetInt("id")
	if userId == 0 {
		c.JSON(http.StatusUnauthorized, gin.H{"status": "unauthorized"})
		return
	}

	depositId := strings.TrimSpace(c.Param("id"))
	var intent model.CryptoDepositIntent
	if err := model.DB.Where("id = ? AND user_id = ?", depositId, userId).First(&intent).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"status": "not_found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"status": "failed"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"status":            intent.Status,
		"usdAdded":          intent.UsdAdded,
		"error":             intent.ErrorMessage,
		"purpose":           intent.Purpose,
		"expectedUsdAmount": intent.ExpectedUsdAmount,
	})
}
