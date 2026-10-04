package controller

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	ethaccounts "github.com/ethereum/go-ethereum/accounts"
	"github.com/ethereum/go-ethereum/common/hexutil"
	ethcrypto "github.com/ethereum/go-ethereum/crypto"
	"github.com/stretchr/testify/require"
)

func TestVerifyEVMWalletSignatureRejectsAnotherWallet(test *testing.T) {
	walletKey, err := ethcrypto.GenerateKey()
	require.NoError(test, err)
	otherKey, err := ethcrypto.GenerateKey()
	require.NoError(test, err)
	intent := &model.CryptoDepositIntent{
		Chain: "arbitrum", WalletAddressFrom: strings.ToLower(ethcrypto.PubkeyToAddress(walletKey.PublicKey).Hex()),
		Challenge: "APIMaster deposit test challenge",
	}
	hash := ethaccounts.TextHash([]byte(intent.Challenge))
	signature, err := ethcrypto.Sign(hash, walletKey)
	require.NoError(test, err)
	require.NoError(test, verifyWalletSignature(intent, hexutil.Encode(signature)))
	signature, err = ethcrypto.Sign(hash, otherKey)
	require.NoError(test, err)
	require.EqualError(test, verifyWalletSignature(intent, hexutil.Encode(signature)), "signature does not match wallet address")
}

func TestVerifyEVMTokenIntentUsesTransferEvent(test *testing.T) {
	wallet := "0x" + strings.Repeat("1", 40)
	recipient := "0x" + strings.Repeat("2", 40)
	relayer := "0x" + strings.Repeat("3", 40)
	delegate := "0x" + strings.Repeat("4", 40)
	token := strings.ToLower(cryptoChains["arbitrum"].usdtAddress)
	txHash := "0x" + strings.Repeat("a", 64)
	testCases := []struct {
		name          string
		txFrom        string
		txTo          string
		logToken      string
		logFrom       string
		logTo         string
		amount        int64
		eventTopic    string
		status        string
		confirmations int64
		omitLog       bool
		shortTopics   bool
		includeFee    bool
		expectedError string
	}{
		{name: "direct transfer"},
		{name: "delegated transfer", txFrom: relayer, txTo: delegate},
		{name: "relayed transfer to token", txFrom: relayer},
		{name: "wallet calls router", txTo: delegate},
		{name: "delegated transfer with fee", txFrom: relayer, txTo: delegate, includeFee: true},
		{name: "wrong token", txFrom: relayer, txTo: delegate, logToken: delegate, expectedError: "matching token transfer not found"},
		{name: "wrong token sender", txFrom: relayer, txTo: delegate, logFrom: relayer, expectedError: "matching token transfer not found"},
		{name: "outer sender cannot replace token sender", logFrom: relayer, expectedError: "matching token transfer not found"},
		{name: "wrong recipient", txFrom: relayer, txTo: delegate, logTo: relayer, expectedError: "matching token transfer not found"},
		{name: "wrong amount", txFrom: relayer, txTo: delegate, amount: 4_999_999, expectedError: "transfer amount mismatch"},
		{name: "wrong event", eventTopic: "0x" + strings.Repeat("b", 64), expectedError: "matching token transfer not found"},
		{name: "missing transfer", omitLog: true, expectedError: "matching token transfer not found"},
		{name: "incomplete topics", shortTopics: true, expectedError: "matching token transfer not found"},
		{name: "failed transaction", status: "0x0", expectedError: "transaction failed on-chain"},
		{name: "insufficient confirmations", confirmations: cryptoEVMConfirmations - 1, expectedError: "transaction confirmations are not sufficient"},
	}
	for _, testCase := range testCases {
		test.Run(testCase.name, func(test *testing.T) {
			txFrom, txTo := wallet, token
			logToken, logFrom, logTo := token, wallet, recipient
			if testCase.txFrom != "" {
				txFrom = testCase.txFrom
			}
			if testCase.txTo != "" {
				txTo = testCase.txTo
			}
			if testCase.logToken != "" {
				logToken = testCase.logToken
			}
			if testCase.logFrom != "" {
				logFrom = testCase.logFrom
			}
			if testCase.logTo != "" {
				logTo = testCase.logTo
			}
			amount := int64(5_000_000)
			if testCase.amount != 0 {
				amount = testCase.amount
			}
			eventTopic := transferEventTopic
			if testCase.eventTopic != "" {
				eventTopic = testCase.eventTopic
			}
			topics := []interface{}{eventTopic, "0x" + strings.Repeat("0", 24) + logFrom[2:], "0x" + strings.Repeat("0", 24) + logTo[2:]}
			if testCase.shortTopics {
				topics = topics[:2]
			}
			logs := []interface{}{map[string]interface{}{
				"address": logToken, "topics": topics, "data": fmt.Sprintf("0x%064x", amount),
			}}
			if testCase.includeFee {
				feeLog := map[string]interface{}{
					"address": token,
					"topics":  []interface{}{transferEventTopic, "0x" + strings.Repeat("0", 24) + wallet[2:], "0x" + strings.Repeat("0", 24) + relayer[2:]},
					"data":    fmt.Sprintf("0x%064x", 30_000),
				}
				logs = append([]interface{}{feeLog}, logs...)
				logs = append(logs, feeLog)
			}
			if testCase.omitLog {
				logs = nil
			}
			status := "0x1"
			if testCase.status != "" {
				status = testCase.status
			}
			confirmations := cryptoEVMConfirmations
			if testCase.confirmations != 0 {
				confirmations = testCase.confirmations
			}
			receipt := map[string]interface{}{"status": status, "blockNumber": "0x100", "logs": logs}
			transaction := map[string]interface{}{"from": txFrom, "to": txTo, "value": "0x0"}
			server := newCryptoEVMTestRPC(test, receipt, transaction, 0x100+confirmations)
			intent := &model.CryptoDepositIntent{
				Chain: "arbitrum", TokenSymbol: "USDT", TokenAddress: token,
				WalletAddressFrom: wallet, ExpectedToAddress: recipient,
				ExpectedBaseUnits: "5000000", TxHash: &txHash,
			}
			usd, err := verifyIntentOnChain(intent, cryptoChains["arbitrum"], []string{server.URL})
			if testCase.expectedError != "" {
				require.EqualError(test, err, testCase.expectedError)
				require.Zero(test, usd)
				return
			}
			require.NoError(test, err)
			require.InDelta(test, 5, usd, 0.000001)
		})
	}
}

func TestVerifyEVMNativeIntentKeepsOuterTransactionChecks(test *testing.T) {
	wallet := "0x" + strings.Repeat("1", 40)
	recipient := "0x" + strings.Repeat("2", 40)
	other := "0x" + strings.Repeat("3", 40)
	txHash := "0x" + strings.Repeat("a", 64)
	testCases := []struct {
		name          string
		from          string
		to            string
		value         string
		expectedError string
	}{
		{name: "direct native transfer", from: wallet, to: recipient, value: "0xde0b6b3a7640000"},
		{name: "wrong sender", from: other, to: recipient, value: "0xde0b6b3a7640000", expectedError: "wallet address mismatch"},
		{name: "wrong recipient", from: wallet, to: other, value: "0xde0b6b3a7640000", expectedError: "recipient mismatch"},
		{name: "zero value", from: wallet, to: recipient, value: "0x0", expectedError: "invalid transfer value"},
		{name: "wrong amount", from: wallet, to: recipient, value: "0xde0b6b3a7640001", expectedError: "transfer amount mismatch"},
	}
	for _, testCase := range testCases {
		test.Run(testCase.name, func(test *testing.T) {
			receipt := map[string]interface{}{"status": "0x1", "blockNumber": "0x100"}
			transaction := map[string]interface{}{"from": testCase.from, "to": testCase.to, "value": testCase.value}
			server := newCryptoEVMTestRPC(test, receipt, transaction, 0x100+cryptoEVMConfirmations)
			intent := &model.CryptoDepositIntent{
				Chain: "arbitrum", TokenSymbol: "ETH", WalletAddressFrom: wallet,
				ExpectedToAddress: recipient, ExpectedBaseUnits: "1000000000000000000",
				AssetUsdPrice: 2000, TxHash: &txHash,
			}
			usd, err := verifyIntentOnChain(intent, cryptoChains["arbitrum"], []string{server.URL})
			if testCase.expectedError != "" {
				require.EqualError(test, err, testCase.expectedError)
				require.Zero(test, usd)
				return
			}
			require.NoError(test, err)
			require.InDelta(test, 2000, usd, 0.000001)
		})
	}
}

func newCryptoEVMTestRPC(test *testing.T, receipt, transaction map[string]interface{}, latestBlock int64) *httptest.Server {
	test.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var payload struct {
			Method string `json:"method"`
		}
		if err := common.DecodeJson(request.Body, &payload); err != nil {
			test.Errorf("decode RPC request: %v", err)
			writer.WriteHeader(http.StatusBadRequest)
			return
		}
		var result interface{}
		switch payload.Method {
		case "eth_getTransactionReceipt":
			result = receipt
		case "eth_getTransactionByHash":
			result = transaction
		case "eth_blockNumber":
			result = fmt.Sprintf("0x%x", latestBlock)
		default:
			test.Errorf("unexpected RPC method: %s", payload.Method)
			writer.WriteHeader(http.StatusBadRequest)
			return
		}
		encoded, err := common.Marshal(map[string]interface{}{"jsonrpc": "2.0", "id": 1, "result": result})
		if err != nil {
			test.Errorf("encode RPC response: %v", err)
			writer.WriteHeader(http.StatusInternalServerError)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write(encoded)
	}))
	test.Cleanup(server.Close)
	return server
}
