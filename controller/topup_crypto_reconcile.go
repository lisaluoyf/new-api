package controller

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/bytedance/gopkg/util/gopool"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	cryptoReconcileInterval        = 15 * time.Second
	cryptoVerificationRetrySeconds = int64(60)
	cryptoReconcileBatchSize       = 100
	cryptoEVMScanBatchBlocks       = int64(40)
	cryptoEVMConfirmations         = int64(12)
)

var (
	cryptoVerificationClaims sync.Map
	cryptoChainScanClaims    sync.Map
)

type cryptoIncomingTransfer struct {
	Chain           string
	TokenSymbol     string
	TokenAddress    string
	From            string
	To              string
	AmountBaseUnits string
	TxHash          string
	BlockTime       int64
}

func StartCryptoDepositReconcileTask() {
	if !common.IsMasterNode {
		return
	}
	gopool.Go(func() {
		runCryptoDepositReconcileCycle()
		ticker := time.NewTicker(cryptoReconcileInterval)
		defer ticker.Stop()
		for range ticker.C {
			runCryptoDepositReconcileCycle()
		}
	})
}

func runCryptoDepositReconcileCycle() {
	expireStaleCryptoIntents()

	var intents []model.CryptoDepositIntent
	now := common.GetTimestamp()
	err := model.DB.Where(
		"status = ? AND tx_hash IS NOT NULL AND recovery_expires_at >= ? AND (next_check_at = 0 OR next_check_at <= ?)",
		model.CryptoDepositIntentStatusPending,
		now,
		now,
	).Order("next_check_at ASC").Limit(cryptoReconcileBatchSize).Find(&intents).Error
	if err != nil {
		common.SysLog("crypto: reconcile query failed: " + err.Error())
	} else {
		for _, intent := range intents {
			intentId := intent.Id
			gopool.Go(func() { runCryptoVerification(intentId) })
		}
	}

	for chain, cfg := range cryptoChains {
		chainName := chain
		chainCfg := cfg
		gopool.Go(func() {
			switch chainCfg.kind {
			case "tron":
				scanTronPlatformWallet(chainName, chainCfg)
			case "solana":
				scanSolanaPlatformWallet(chainName, chainCfg)
			default:
				scanEVMPlatformWallet(chainName, chainCfg)
			}
		})
	}
}

func runCryptoVerification(intentId string) {
	if strings.TrimSpace(intentId) == "" {
		return
	}
	if _, loaded := cryptoVerificationClaims.LoadOrStore(intentId, struct{}{}); loaded {
		return
	}
	defer cryptoVerificationClaims.Delete(intentId)
	verifyAndCredit(intentId)
}

func expireStaleCryptoIntents() {
	now := common.GetTimestamp()
	var intents []model.CryptoDepositIntent
	err := model.DB.Where(
		"status = ? AND recovery_expires_at > 0 AND ((wallet_signature = '' AND expires_at < ?) OR (wallet_signature <> '' AND recovery_expires_at < ?))",
		model.CryptoDepositIntentStatusPending,
		now,
		now,
	).Limit(cryptoReconcileBatchSize).Find(&intents).Error
	if err != nil {
		common.SysLog("crypto: expired intent query failed: " + err.Error())
		return
	}
	for _, intent := range intents {
		markCryptoIntentExpired(&intent)
	}
}

func markCryptoIntentExpired(intent *model.CryptoDepositIntent) {
	if intent == nil {
		return
	}
	err := model.DB.Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&model.CryptoDepositIntent{}).
			Where("id = ? AND status = ?", intent.Id, model.CryptoDepositIntentStatusPending).
			Updates(map[string]interface{}{
				"status":        model.CryptoDepositIntentStatusExpired,
				"error_message": "deposit intent expired",
				"next_check_at": 0,
			})
		if result.Error != nil || result.RowsAffected == 0 {
			return result.Error
		}
		if intent.Purpose == cryptoIntentPurposeWalletTopup && intent.TopUpTradeNo != "" {
			return tx.Model(&model.TopUp{}).
				Where("trade_no = ? AND payment_provider = ? AND status = ?", intent.TopUpTradeNo, model.PaymentProviderCrypto, common.TopUpStatusPending).
				Update("status", common.TopUpStatusExpired).Error
		}
		return nil
	})
	if err != nil {
		common.SysLog(fmt.Sprintf("crypto: expire intent failed intent=%s err=%v", intent.Id, err))
	}
}

func scanEVMPlatformWallet(chain string, cfg cryptoChainConfig) {
	if _, loaded := cryptoChainScanClaims.LoadOrStore(chain, struct{}{}); loaded {
		return
	}
	defer cryptoChainScanClaims.Delete(chain)

	now := common.GetTimestamp()
	var activeCount int64
	if err := model.DB.Model(&model.CryptoDepositIntent{}).Where(
		"chain = ? AND status = ? AND wallet_signature <> '' AND tx_hash IS NULL AND recovery_expires_at >= ?",
		chain,
		model.CryptoDepositIntentStatusPending,
		now,
	).Count(&activeCount).Error; err != nil {
		common.SysLog(fmt.Sprintf("crypto: active recovery count failed chain=%s err=%v", chain, err))
		return
	}

	rpcURL, latestBlock, err := latestEVMBlock(cfg)
	if err != nil {
		common.SysLog(fmt.Sprintf("crypto: latest block failed chain=%s err=%v", chain, err))
		return
	}
	targetBlock := latestBlock - cryptoEVMConfirmations
	if targetBlock < 0 {
		return
	}

	cursor, exists, err := loadCryptoChainCursor(chain)
	if err != nil {
		common.SysLog(fmt.Sprintf("crypto: load scan cursor failed chain=%s err=%v", chain, err))
		return
	}
	if !exists {
		var earliestAuthorized int64
		if err := model.DB.Model(&model.CryptoDepositIntent{}).
			Where("chain = ? AND status = ? AND wallet_signature <> '' AND tx_hash IS NULL AND recovery_expires_at >= ?", chain, model.CryptoDepositIntentStatusPending, now).
			Select("MIN(authorized_at)").Scan(&earliestAuthorized).Error; err != nil {
			common.SysLog(fmt.Sprintf("crypto: find earliest authorized intent failed chain=%s err=%v", chain, err))
			return
		}
		lookbackSeconds := int64(0)
		if earliestAuthorized > 0 && now > earliestAuthorized {
			lookbackSeconds = now - earliestAuthorized
			if lookbackSeconds > 24*60*60 {
				lookbackSeconds = 24 * 60 * 60
			}
		}
		cursor = targetBlock - cryptoEVMBlocksForSeconds(chain, lookbackSeconds) - cryptoEVMConfirmations
		if cursor < 0 {
			cursor = 0
		}
	}
	if activeCount == 0 {
		if err := saveCryptoChainCursor(chain, targetBlock); err != nil {
			common.SysLog(fmt.Sprintf("crypto: advance idle scan cursor failed chain=%s err=%v", chain, err))
		}
		return
	}
	if cursor >= targetBlock {
		return
	}

	endBlock := cursor + cryptoEVMScanBatchBlocks
	if endBlock > targetBlock {
		endBlock = targetBlock
	}
	for blockNumber := cursor + 1; blockNumber <= endBlock; blockNumber++ {
		if err := scanEVMBlock(rpcURL, chain, cfg, blockNumber); err != nil {
			common.SysLog(fmt.Sprintf("crypto: scan block failed chain=%s block=%d err=%v", chain, blockNumber, err))
			return
		}
		if err := saveCryptoChainCursor(chain, blockNumber); err != nil {
			common.SysLog(fmt.Sprintf("crypto: save scan cursor failed chain=%s block=%d err=%v", chain, blockNumber, err))
			return
		}
	}
}

func cryptoEVMBlocksForSeconds(chain string, seconds int64) int64 {
	if seconds <= 0 {
		return 0
	}
	blockSeconds := int64(12)
	switch chain {
	case "bsc":
		blockSeconds = 3
	case "polygon":
		blockSeconds = 2
	case "arbitrum":
		blockSeconds = 1
	case "base":
		blockSeconds = 2
	}
	blocks := seconds / blockSeconds
	if seconds%blockSeconds != 0 {
		blocks++
	}
	return blocks
}

func latestEVMBlock(cfg cryptoChainConfig) (string, int64, error) {
	var lastErr error
	for _, rpcURL := range getRPCs(cfg) {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		result, err := ethCall(ctx, rpcURL, "eth_blockNumber", nil)
		cancel()
		if err != nil {
			lastErr = err
			continue
		}
		value, ok := result.(string)
		if !ok {
			lastErr = fmt.Errorf("unexpected block number response")
			continue
		}
		block, ok := hexToInt64(value)
		if !ok {
			lastErr = fmt.Errorf("invalid block number")
			continue
		}
		return rpcURL, block, nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no RPC configured")
	}
	return "", 0, lastErr
}

func loadCryptoChainCursor(chain string) (int64, bool, error) {
	var record model.CryptoChainScanCursor
	err := model.DB.Where("chain = ?", chain).First(&record).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	value, parseErr := strconv.ParseInt(record.Cursor, 10, 64)
	if parseErr != nil || value < 0 {
		return 0, false, fmt.Errorf("invalid cursor %q", record.Cursor)
	}
	return value, true, nil
}

func saveCryptoChainCursor(chain string, blockNumber int64) error {
	record := model.CryptoChainScanCursor{
		Chain: chain, Cursor: strconv.FormatInt(blockNumber, 10), UpdatedAt: common.GetTimestamp(),
	}
	return model.DB.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "chain"}},
		DoUpdates: clause.AssignmentColumns([]string{"cursor", "updated_at"}),
	}).Create(&record).Error
}

func scanEVMBlock(rpcURL, chain string, cfg cryptoChainConfig, blockNumber int64) error {
	blockHex := fmt.Sprintf("0x%x", blockNumber)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	result, err := ethCall(ctx, rpcURL, "eth_getBlockByNumber", []interface{}{blockHex, true})
	cancel()
	if err != nil || result == nil {
		if err == nil {
			err = fmt.Errorf("block unavailable")
		}
		return err
	}
	block, ok := result.(map[string]interface{})
	if !ok {
		return fmt.Errorf("unexpected block response")
	}
	blockTime, _ := hexToInt64(stringValue(block["timestamp"]))
	platformWallet := strings.ToLower(getPlatformWalletForChain(chain))
	transactions, _ := block["transactions"].([]interface{})
	for _, raw := range transactions {
		tx, _ := raw.(map[string]interface{})
		if strings.ToLower(stringValue(tx["to"])) != platformWallet {
			continue
		}
		amount, ok := hexToDecimal(stringValue(tx["value"]))
		if !ok || amount.Sign() <= 0 {
			continue
		}
		matchAndBindIncomingTransfer(cryptoIncomingTransfer{
			Chain: chain, TokenSymbol: nativeTokenSymbol(chain), From: strings.ToLower(stringValue(tx["from"])),
			To: platformWallet, AmountBaseUnits: amount.String(), TxHash: strings.ToLower(stringValue(tx["hash"])),
			BlockTime: blockTime,
		})
	}

	recipientTopic := "0x" + strings.Repeat("0", 24) + strings.TrimPrefix(platformWallet, "0x")
	ctx, cancel = context.WithTimeout(context.Background(), 20*time.Second)
	logsResult, err := ethCall(ctx, rpcURL, "eth_getLogs", []interface{}{map[string]interface{}{
		"fromBlock": blockHex,
		"toBlock":   blockHex,
		"topics":    []interface{}{transferEventTopic, nil, recipientTopic},
	}})
	cancel()
	if err != nil {
		return err
	}
	logs, _ := logsResult.([]interface{})
	for _, raw := range logs {
		entry, _ := raw.(map[string]interface{})
		tokenAddress := strings.ToLower(stringValue(entry["address"]))
		tokenSymbol := tokenSymbolForAddress(cfg, tokenAddress)
		if tokenSymbol == "" {
			continue
		}
		topics, _ := entry["topics"].([]interface{})
		if len(topics) < 3 {
			continue
		}
		amount, ok := hexToDecimal(stringValue(entry["data"]))
		if !ok || amount.Sign() <= 0 {
			continue
		}
		matchAndBindIncomingTransfer(cryptoIncomingTransfer{
			Chain: chain, TokenSymbol: tokenSymbol, TokenAddress: tokenAddress,
			From: normalizeTopicAddress(stringValue(topics[1])), To: platformWallet,
			AmountBaseUnits: amount.String(), TxHash: strings.ToLower(stringValue(entry["transactionHash"])),
			BlockTime: blockTime,
		})
	}
	return nil
}

func stringValue(value interface{}) string {
	text, _ := value.(string)
	return strings.TrimSpace(text)
}

func hexToInt64(value string) (int64, bool) {
	number, ok := new(big.Int).SetString(strings.TrimPrefix(strings.TrimSpace(value), "0x"), 16)
	if !ok || !number.IsInt64() {
		return 0, false
	}
	return number.Int64(), true
}

func nativeTokenSymbol(chain string) string {
	switch chain {
	case "bsc":
		return "BNB"
	case "polygon":
		return "POL"
	default:
		return "ETH"
	}
}

func tokenSymbolForAddress(cfg cryptoChainConfig, address string) string {
	address = strings.ToLower(strings.TrimSpace(address))
	if address != "" && address == strings.ToLower(cfg.usdtAddress) {
		return "USDT"
	}
	if address != "" && address == strings.ToLower(cfg.usdcAddress) {
		return "USDC"
	}
	return ""
}

func matchAndBindIncomingTransfer(transfer cryptoIncomingTransfer) {
	if transfer.BlockTime <= 0 || transfer.TxHash == "" || transfer.AmountBaseUnits == "" {
		return
	}
	intent, unique, err := findUniqueCryptoRecoveryCandidate(transfer)
	if err != nil {
		common.SysLog(fmt.Sprintf("crypto: recovery match query failed chain=%s tx=%s err=%v", transfer.Chain, transfer.TxHash, err))
		return
	}
	if !unique {
		return
	}
	bound, err := bindRecoveredCryptoHash(intent.Id, transfer.Chain, transfer.TxHash, transfer.BlockTime)
	if err != nil {
		common.SysLog(fmt.Sprintf("crypto: recovery bind failed intent=%s tx=%s err=%v", intent.Id, transfer.TxHash, err))
		return
	}
	if !bound {
		return
	}
	intent.TxHash = &transfer.TxHash
	intent.TxSubmittedAt = transfer.BlockTime
	recordCryptoIntentLog(
		intent.UserId,
		fmt.Sprintf("自动找回加密货币交易：%s %s，交易 %s，等待链上核验", strings.ToUpper(intent.Chain), intent.TokenSymbol, cryptoShortValue(transfer.TxHash)),
		intent,
		map[string]interface{}{"stage": "recovery_matched"},
	)
	runCryptoVerification(intent.Id)
}

func findUniqueCryptoRecoveryCandidate(transfer cryptoIncomingTransfer) (*model.CryptoDepositIntent, bool, error) {
	query := model.DB.Where(
		"chain = ? AND token_symbol = ? AND token_address = ? AND wallet_address_from = ? AND expected_to_address = ? AND expected_base_units = ? AND status = ? AND wallet_signature <> '' AND authorized_at > 0 AND tx_hash IS NULL AND created_at <= ? AND authorized_at <= ? AND recovery_expires_at >= ?",
		transfer.Chain,
		transfer.TokenSymbol,
		transfer.TokenAddress,
		transfer.From,
		transfer.To,
		transfer.AmountBaseUnits,
		model.CryptoDepositIntentStatusPending,
		transfer.BlockTime+120,
		transfer.BlockTime,
		transfer.BlockTime,
	)
	var candidates []model.CryptoDepositIntent
	if err := query.Order("created_at ASC").Limit(2).Find(&candidates).Error; err != nil {
		return nil, false, err
	}
	if len(candidates) != 1 {
		return nil, false, nil
	}
	return &candidates[0], true, nil
}

func bindRecoveredCryptoHash(intentId, chain, txHash string, submittedAt int64) (bool, error) {
	bound := false
	err := model.DB.Transaction(func(tx *gorm.DB) error {
		var existing model.CryptoDepositIntent
		err := tx.Where("chain = ? AND tx_hash = ?", chain, txHash).First(&existing).Error
		if err == nil {
			return nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}

		var intent model.CryptoDepositIntent
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", intentId).First(&intent).Error; err != nil {
			return err
		}
		if intent.Status != model.CryptoDepositIntentStatusPending || intent.TxHash != nil || intent.WalletSignature == "" || intent.RecoveryExpiresAt < submittedAt {
			return nil
		}
		intent.TxHash = &txHash
		now := common.GetTimestamp()
		intent.TxSubmittedAt = submittedAt
		intent.RecoveryExpiresAt = now + int64(cryptoHashVerificationWindow/time.Second)
		intent.NextCheckAt = now
		if err := tx.Save(&intent).Error; err != nil {
			return err
		}
		bound = true
		return nil
	})
	if isDuplicateDBError(err) {
		return false, nil
	}
	return bound, err
}

func activeCryptoRecoveryCount(chain string, now int64) (int64, error) {
	var count int64
	err := model.DB.Model(&model.CryptoDepositIntent{}).Where(
		"chain = ? AND status = ? AND wallet_signature <> '' AND tx_hash IS NULL AND recovery_expires_at >= ?",
		chain,
		model.CryptoDepositIntentStatusPending,
		now,
	).Count(&count).Error
	return count, err
}

func loadRawCryptoChainCursor(chain string) (string, bool, error) {
	var record model.CryptoChainScanCursor
	err := model.DB.Where("chain = ?", chain).First(&record).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return record.Cursor, true, nil
}

func saveRawCryptoChainCursor(chain, cursor string) error {
	record := model.CryptoChainScanCursor{Chain: chain, Cursor: cursor, UpdatedAt: common.GetTimestamp()}
	return model.DB.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "chain"}},
		DoUpdates: clause.AssignmentColumns([]string{"cursor", "updated_at"}),
	}).Create(&record).Error
}

func scanTronPlatformWallet(chain string, cfg cryptoChainConfig) {
	if _, loaded := cryptoChainScanClaims.LoadOrStore(chain, struct{}{}); loaded {
		return
	}
	defer cryptoChainScanClaims.Delete(chain)

	now := common.GetTimestamp()
	activeCount, err := activeCryptoRecoveryCount(chain, now)
	if err != nil {
		common.SysLog(fmt.Sprintf("crypto: active recovery count failed chain=%s err=%v", chain, err))
		return
	}
	cursorText, exists, err := loadRawCryptoChainCursor(chain)
	if err != nil {
		common.SysLog(fmt.Sprintf("crypto: load scan cursor failed chain=%s err=%v", chain, err))
		return
	}
	cursorMs := time.Now().Add(-30 * time.Minute).UnixMilli()
	if exists {
		if parsed, parseErr := strconv.ParseInt(cursorText, 10, 64); parseErr == nil && parsed > 0 {
			cursorMs = parsed
		}
	}
	if activeCount == 0 {
		_ = saveRawCryptoChainCursor(chain, strconv.FormatInt(time.Now().UnixMilli(), 10))
		return
	}

	platformWallet := getPlatformWalletForChain(chain)
	maxTimestamp, err := scanTronNativeTransfers(cfg, platformWallet, cursorMs)
	if err != nil {
		common.SysLog(fmt.Sprintf("crypto: TRON native scan failed err=%v", err))
		return
	}
	tokenTimestamp, err := scanTronTokenTransfers(cfg, platformWallet, cursorMs)
	if err != nil {
		common.SysLog(fmt.Sprintf("crypto: TRON token scan failed err=%v", err))
		return
	}
	if tokenTimestamp > maxTimestamp {
		maxTimestamp = tokenTimestamp
	}
	safeWatermark := time.Now().Add(-5 * time.Minute).UnixMilli()
	if safeWatermark > maxTimestamp {
		maxTimestamp = safeWatermark
	}
	if maxTimestamp > cursorMs {
		_ = saveRawCryptoChainCursor(chain, strconv.FormatInt(maxTimestamp, 10))
	}
}

func tronAPIGet(cfg cryptoChainConfig, path string, query url.Values, result interface{}) error {
	var lastErr error
	for _, endpoint := range getRPCs(cfg) {
		requestURL := strings.TrimRight(endpoint, "/") + path
		if encoded := query.Encode(); encoded != "" {
			requestURL += "?" + encoded
		}
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
		if err == nil {
			if apiKey := strings.TrimSpace(os.Getenv("TRONGRID_API_KEY")); apiKey != "" {
				req.Header.Set("TRON-PRO-API-KEY", apiKey)
			}
			var resp *http.Response
			resp, err = (&http.Client{Timeout: 20 * time.Second}).Do(req)
			if err == nil {
				if resp.StatusCode >= 200 && resp.StatusCode < 300 {
					err = common.DecodeJson(resp.Body, result)
				} else {
					body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
					err = fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
				}
				resp.Body.Close()
			}
		}
		cancel()
		if err == nil {
			return nil
		}
		lastErr = err
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no TRON API configured")
	}
	return lastErr
}

func scanTronNativeTransfers(cfg cryptoChainConfig, platformWallet string, cursorMs int64) (int64, error) {
	type tronNativeResponse struct {
		Data []struct {
			TxID           string `json:"txID"`
			BlockTimestamp int64  `json:"block_timestamp"`
			RawData        struct {
				Contract []struct {
					Type      string `json:"type"`
					Parameter struct {
						Value struct {
							Amount       int64  `json:"amount"`
							OwnerAddress string `json:"owner_address"`
							ToAddress    string `json:"to_address"`
						} `json:"value"`
					} `json:"parameter"`
				} `json:"contract"`
			} `json:"raw_data"`
		} `json:"data"`
	}
	var response tronNativeResponse
	err := tronAPIGet(cfg, "/v1/accounts/"+url.PathEscape(platformWallet)+"/transactions", url.Values{
		"only_confirmed": {"true"}, "only_to": {"true"}, "limit": {"200"},
		"min_timestamp": {strconv.FormatInt(cursorMs+1, 10)}, "order_by": {"block_timestamp,asc"},
	}, &response)
	if err != nil {
		return cursorMs, err
	}
	maxTimestamp := cursorMs
	for _, transaction := range response.Data {
		if transaction.BlockTimestamp > maxTimestamp {
			maxTimestamp = transaction.BlockTimestamp
		}
		if len(transaction.RawData.Contract) == 0 {
			continue
		}
		contract := transaction.RawData.Contract[0]
		if contract.Type != "TransferContract" || contract.Parameter.Value.Amount <= 0 {
			continue
		}
		from := tronHexAddressToBase58(contract.Parameter.Value.OwnerAddress)
		to := tronHexAddressToBase58(contract.Parameter.Value.ToAddress)
		if to != platformWallet || from == "" {
			continue
		}
		matchAndBindIncomingTransfer(cryptoIncomingTransfer{
			Chain: "tron", TokenSymbol: "TRX", From: from, To: to,
			AmountBaseUnits: strconv.FormatInt(contract.Parameter.Value.Amount, 10),
			TxHash:          transaction.TxID, BlockTime: transaction.BlockTimestamp / 1000,
		})
	}
	return maxTimestamp, nil
}

func scanTronTokenTransfers(cfg cryptoChainConfig, platformWallet string, cursorMs int64) (int64, error) {
	type tronTokenResponse struct {
		Data []struct {
			TransactionID string `json:"transaction_id"`
			BlockTime     int64  `json:"block_timestamp"`
			From          string `json:"from"`
			To            string `json:"to"`
			Value         string `json:"value"`
			Type          string `json:"type"`
			TokenInfo     struct {
				Address string `json:"address"`
				Symbol  string `json:"symbol"`
			} `json:"token_info"`
		} `json:"data"`
	}
	var response tronTokenResponse
	err := tronAPIGet(cfg, "/v1/accounts/"+url.PathEscape(platformWallet)+"/transactions/trc20", url.Values{
		"only_confirmed": {"true"}, "limit": {"200"},
		"min_timestamp": {strconv.FormatInt(cursorMs+1, 10)}, "order_by": {"block_timestamp,asc"},
	}, &response)
	if err != nil {
		return cursorMs, err
	}
	maxTimestamp := cursorMs
	for _, transaction := range response.Data {
		if transaction.BlockTime > maxTimestamp {
			maxTimestamp = transaction.BlockTime
		}
		if !strings.EqualFold(transaction.Type, "Transfer") || transaction.To != platformWallet || transaction.Value == "" {
			continue
		}
		if transaction.TokenInfo.Address != cfg.usdtAddress || !strings.EqualFold(transaction.TokenInfo.Symbol, "USDT") {
			continue
		}
		matchAndBindIncomingTransfer(cryptoIncomingTransfer{
			Chain: "tron", TokenSymbol: "USDT", TokenAddress: transaction.TokenInfo.Address,
			From: transaction.From, To: transaction.To, AmountBaseUnits: transaction.Value,
			TxHash: transaction.TransactionID, BlockTime: transaction.BlockTime / 1000,
		})
	}
	return maxTimestamp, nil
}

func tronHexAddressToBase58(value string) string {
	decoded, ok := new(big.Int).SetString(strings.TrimPrefix(strings.TrimSpace(value), "0x"), 16)
	if !ok {
		return ""
	}
	bytesValue := decoded.Bytes()
	if len(bytesValue) < 21 {
		padding := make([]byte, 21-len(bytesValue))
		bytesValue = append(padding, bytesValue...)
	}
	if len(bytesValue) != 21 || bytesValue[0] != 0x41 {
		return ""
	}
	return tronAddressFromBytes(bytesValue)
}

func scanSolanaPlatformWallet(chain string, cfg cryptoChainConfig) {
	if _, loaded := cryptoChainScanClaims.LoadOrStore(chain, struct{}{}); loaded {
		return
	}
	defer cryptoChainScanClaims.Delete(chain)

	now := common.GetTimestamp()
	activeCount, err := activeCryptoRecoveryCount(chain, now)
	if err != nil {
		common.SysLog(fmt.Sprintf("crypto: active recovery count failed chain=%s err=%v", chain, err))
		return
	}
	platformWallet := getPlatformWalletForChain(chain)
	if platformWallet == "" {
		return
	}
	if activeCount == 0 {
		_, signatures, signatureErr := getSolanaSignatures(cfg, platformWallet)
		if signatureErr == nil && len(signatures) > 0 {
			_ = saveRawCryptoChainCursor("solana_native", stringValue(signatures[0]["signature"]))
		}
		return
	}

	monitoredAddresses := []string{platformWallet}
	tokenAccounts, err := getSolanaTokenAccounts(cfg, platformWallet, cfg.usdtAddress)
	if err != nil {
		common.SysLog(fmt.Sprintf("crypto: Solana token account lookup failed err=%v", err))
		return
	}
	monitoredAddresses = append(monitoredAddresses, tokenAccounts...)
	for index, address := range monitoredAddresses {
		cursorKey := "solana_native"
		if index > 0 {
			cursorKey = solanaTokenCursorKey(address)
		}
		if err := scanSolanaMonitoredAddress(cfg, platformWallet, address, cursorKey); err != nil {
			common.SysLog(fmt.Sprintf("crypto: Solana address scan failed address=%s err=%v", address, err))
			return
		}
	}
}

func solanaTokenCursorKey(address string) string {
	digest := sha256.Sum256([]byte(strings.TrimSpace(address)))
	return fmt.Sprintf("solana_usdt_%x", digest[:8])
}

func scanSolanaMonitoredAddress(cfg cryptoChainConfig, platformWallet, monitoredAddress, cursorKey string) error {
	cursor, exists, err := loadRawCryptoChainCursor(cursorKey)
	if err != nil {
		return err
	}
	rpcURL, signatures, err := getSolanaSignatures(cfg, monitoredAddress)
	if err != nil || len(signatures) == 0 {
		return err
	}
	newestSignature := stringValue(signatures[0]["signature"])
	pending := make([]map[string]interface{}, 0, len(signatures))
	for _, signature := range signatures {
		if exists && stringValue(signature["signature"]) == cursor {
			break
		}
		pending = append(pending, signature)
	}
	for index := len(pending) - 1; index >= 0; index-- {
		signature := pending[index]
		if signature["err"] != nil {
			continue
		}
		txHash := stringValue(signature["signature"])
		blockTimeFloat, _ := signature["blockTime"].(float64)
		if err := scanSolanaTransaction(rpcURL, cfg, platformWallet, txHash, int64(blockTimeFloat)); err != nil {
			return fmt.Errorf("transaction %s: %w", txHash, err)
		}
	}
	return saveRawCryptoChainCursor(cursorKey, newestSignature)
}

func getSolanaTokenAccounts(cfg cryptoChainConfig, owner, mint string) ([]string, error) {
	var lastErr error
	for _, rpcURL := range getRPCs(cfg) {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		result, err := ethCall(ctx, rpcURL, "getTokenAccountsByOwner", []interface{}{
			owner,
			map[string]interface{}{"mint": mint},
			map[string]interface{}{"encoding": "jsonParsed", "commitment": "finalized"},
		})
		cancel()
		if err != nil {
			lastErr = err
			continue
		}
		resultMap, _ := result.(map[string]interface{})
		values, _ := resultMap["value"].([]interface{})
		accounts := make([]string, 0, len(values))
		for _, rawValue := range values {
			value, _ := rawValue.(map[string]interface{})
			if account := stringValue(value["pubkey"]); account != "" {
				accounts = append(accounts, account)
			}
		}
		return accounts, nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no Solana RPC configured")
	}
	return nil, lastErr
}

func getSolanaSignatures(cfg cryptoChainConfig, platformWallet string) (string, []map[string]interface{}, error) {
	var lastErr error
	for _, rpcURL := range getRPCs(cfg) {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		result, err := ethCall(ctx, rpcURL, "getSignaturesForAddress", []interface{}{platformWallet, map[string]interface{}{"limit": 100, "commitment": "finalized"}})
		cancel()
		if err != nil {
			lastErr = err
			continue
		}
		rawItems, _ := result.([]interface{})
		items := make([]map[string]interface{}, 0, len(rawItems))
		for _, rawItem := range rawItems {
			if item, ok := rawItem.(map[string]interface{}); ok {
				items = append(items, item)
			}
		}
		return rpcURL, items, nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no Solana RPC configured")
	}
	return "", nil, lastErr
}

func scanSolanaTransaction(rpcURL string, cfg cryptoChainConfig, platformWallet, txHash string, blockTime int64) error {
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	result, err := ethCall(ctx, rpcURL, "getTransaction", []interface{}{txHash, map[string]interface{}{
		"encoding": "jsonParsed", "commitment": "finalized", "maxSupportedTransactionVersion": 0,
	}})
	cancel()
	if err != nil || result == nil {
		if err == nil {
			err = fmt.Errorf("transaction unavailable")
		}
		return err
	}
	txResult, _ := result.(map[string]interface{})
	meta, _ := txResult["meta"].(map[string]interface{})
	if meta == nil || meta["err"] != nil {
		return nil
	}
	transaction, _ := txResult["transaction"].(map[string]interface{})
	message, _ := transaction["message"].(map[string]interface{})
	accountKeys, _ := message["accountKeys"].([]interface{})
	if len(accountKeys) == 0 {
		return nil
	}
	from := solanaAccountKey(accountKeys[0])
	instructions, _ := message["instructions"].([]interface{})
	for _, rawInstruction := range instructions {
		instruction, _ := rawInstruction.(map[string]interface{})
		parsed, _ := instruction["parsed"].(map[string]interface{})
		if parsed["type"] != "transfer" {
			continue
		}
		info, _ := parsed["info"].(map[string]interface{})
		if stringValue(info["source"]) != from || stringValue(info["destination"]) != platformWallet {
			continue
		}
		lamports, ok := numericBaseUnits(info["lamports"])
		if !ok {
			continue
		}
		matchAndBindIncomingTransfer(cryptoIncomingTransfer{
			Chain: "solana", TokenSymbol: "SOL", From: from, To: platformWallet,
			AmountBaseUnits: lamports, TxHash: txHash, BlockTime: blockTime,
		})
	}

	type tokenBalance struct {
		owner  string
		mint   string
		amount *big.Int
	}
	parseBalances := func(raw interface{}) map[int]tokenBalance {
		balances := make(map[int]tokenBalance)
		items, _ := raw.([]interface{})
		for _, item := range items {
			balance, _ := item.(map[string]interface{})
			indexFloat, _ := balance["accountIndex"].(float64)
			uiAmount, _ := balance["uiTokenAmount"].(map[string]interface{})
			amount, ok := new(big.Int).SetString(stringValue(uiAmount["amount"]), 10)
			if !ok {
				continue
			}
			balances[int(indexFloat)] = tokenBalance{
				owner: stringValue(balance["owner"]), mint: stringValue(balance["mint"]), amount: amount,
			}
		}
		return balances
	}
	pre := parseBalances(meta["preTokenBalances"])
	post := parseBalances(meta["postTokenBalances"])
	receivedByMint := make(map[string]*big.Int)
	sentByMint := make(map[string]*big.Int)
	for index, after := range post {
		beforeAmount := big.NewInt(0)
		if before, ok := pre[index]; ok && before.amount != nil {
			beforeAmount = before.amount
		}
		delta := new(big.Int).Sub(after.amount, beforeAmount)
		if after.owner == platformWallet && delta.Sign() > 0 {
			if receivedByMint[after.mint] == nil {
				receivedByMint[after.mint] = big.NewInt(0)
			}
			receivedByMint[after.mint].Add(receivedByMint[after.mint], delta)
		}
		if after.owner == from && delta.Sign() < 0 {
			if sentByMint[after.mint] == nil {
				sentByMint[after.mint] = big.NewInt(0)
			}
			sentByMint[after.mint].Sub(sentByMint[after.mint], delta)
		}
	}
	for mint, received := range receivedByMint {
		sent := sentByMint[mint]
		if mint != cfg.usdtAddress || received.Sign() <= 0 || sent == nil || sent.Cmp(received) < 0 {
			continue
		}
		matchAndBindIncomingTransfer(cryptoIncomingTransfer{
			Chain: "solana", TokenSymbol: "USDT", TokenAddress: mint, From: from, To: platformWallet,
			AmountBaseUnits: received.String(), TxHash: txHash, BlockTime: blockTime,
		})
	}
	return nil
}

func numericBaseUnits(value interface{}) (string, bool) {
	switch number := value.(type) {
	case string:
		parsed, ok := new(big.Int).SetString(number, 10)
		return number, ok && parsed.Sign() > 0
	case float64:
		if number <= 0 || number != float64(int64(number)) {
			return "", false
		}
		return strconv.FormatInt(int64(number), 10), true
	default:
		return "", false
	}
}
