package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/bytedance/gopkg/util/gopool"
)

var trialLimitNotificationHTTPClient = &http.Client{Timeout: 10 * time.Second}

// EnqueueGPTTrialLimitNotification is called only after a successful request
// settlement. It deliberately has no historical scan: launching this feature
// must not contact users who crossed the threshold before it existed.
func EnqueueGPTTrialLimitNotification(userID, subscriptionID int) {
	if userID <= 0 || subscriptionID <= 0 {
		return
	}
	gopool.Go(func() {
		notification, err := model.CreateTrialLimitNotificationIfReached(userID, subscriptionID)
		if err != nil {
			common.SysError(fmt.Sprintf("trial-limit notification enqueue failed user_id=%d subscription_id=%d: %v", userID, subscriptionID, err))
			return
		}
		if notification == nil {
			return
		}
		deliverGPTTrialLimitNotification(notification)
	})
}

func deliverGPTTrialLimitNotification(notification *model.TrialLimitNotification) {
	if notification == nil {
		return
	}
	user, err := model.GetUserById(notification.UserId, false)
	if err != nil {
		_ = model.MarkTrialLimitNotificationFailed(notification.Id, err)
		return
	}
	variant := model.TrialLimitNotificationStandard
	if eligible, _ := model.IsFirstTopupPromoEligible(user.Id); eligible {
		variant = model.TrialLimitNotificationFirstTopupPromo
	}
	text, button := renderGPTTrialLimitNotification(variant, user.Language)
	url := trialLimitNotificationRedirectURL(notification.ClickToken)

	telegramID, telegramOK := parseNumericSocialID(user.TelegramId)
	discordID, discordOK := parseNumericSocialID(user.DiscordId)
	var deliveryErrors []string
	if telegramOK {
		if err := sendTrialLimitTelegramMessage(telegramID, text, button, url); err == nil {
			_ = model.MarkTrialLimitNotificationSent(notification.Id, model.TrialLimitNotificationTelegram, variant, "")
			return
		} else {
			deliveryErrors = append(deliveryErrors, "telegram: "+err.Error())
		}
	}
	if discordOK {
		if err := sendTrialLimitDiscordMessage(discordID, text, button, url); err == nil {
			_ = model.MarkTrialLimitNotificationSent(notification.Id, model.TrialLimitNotificationDiscord, variant, "")
			return
		} else {
			deliveryErrors = append(deliveryErrors, "discord: "+err.Error())
		}
	}
	if !telegramOK && !discordOK {
		_ = model.MarkTrialLimitNotificationUnavailable(notification.Id, nil)
		return
	}
	_ = model.MarkTrialLimitNotificationFailed(notification.Id, errors.New(strings.Join(deliveryErrors, "; ")))
}

func parseNumericSocialID(value string) (string, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", false
	}
	parsed, err := strconv.ParseUint(value, 10, 64)
	if err != nil || parsed == 0 {
		return "", false
	}
	return value, true
}

func trialLimitNotificationRedirectURL(token string) string {
	base := strings.TrimRight(strings.TrimSpace(system_setting.ServerAddress), "/")
	if base == "" || strings.HasPrefix(base, "http://localhost") {
		base = "https://apimaster.ai"
	}
	return base + "/api/trial-limit/redirect/" + token
}

type trialLimitNotificationCopy struct {
	title          string
	continueText   string
	pricingText    string
	keyText        string
	modelsText     string
	standardButton string
	promoLine      string
	promoButton    string
}

func trialLimitNotificationCopyFor(language string) trialLimitNotificationCopy {
	locale := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(language), "_", "-"))
	if locale == "zh-tw" || strings.HasPrefix(locale, "zh-tw-") ||
		locale == "zh-hk" || strings.HasPrefix(locale, "zh-hk-") ||
		locale == "zh-mo" || strings.HasPrefix(locale, "zh-mo-") {
		return trialLimitNotificationCopy{
			title:          "⚠️ GPT 體驗額度即將用盡",
			continueText:   "💳 繼續使用需要儲值錢包餘額。",
			pricingText:    "💰 體驗額度按官方價格計費；錢包餘額按折扣價計費，例如 GPT-5.6 可節省 97%。",
			keyText:        "🔑 儲值後無需重新設定，現在的 API Key 即可繼續使用。",
			modelsText:     "🤖 一個 API Key 即可使用 GPT、Claude、Gemini、Kimi、GLM、DeepSeek 等主流模型。",
			standardButton: "🚀 立即儲值並繼續使用",
			promoLine:      "🎁 首次儲值限時優惠：儲值 $%d，僅需支付 $%.2f（%d 折）。",
			promoButton:    "🚀 立即儲值，享 %d 折",
		}
	}
	if locale == "zh" || strings.HasPrefix(locale, "zh-") {
		return trialLimitNotificationCopy{
			title:          "⚠️ GPT 体验额度即将用尽",
			continueText:   "💳 继续调用需要充值钱包余额。",
			pricingText:    "💰 体验额度按官方价格计费；钱包余额按折扣价计费，例如 GPT-5.6 可节省 97%。",
			keyText:        "🔑 充值后，无需重新配置，当前 API Key 即可继续使用。",
			modelsText:     "🤖 一个 API Key 即可使用 GPT、Claude、Gemini、Kimi、GLM、DeepSeek 等主流模型。",
			standardButton: "🚀 立即充值并继续使用",
			promoLine:      "🎁 首充限时优惠：充值 $%d，仅需支付 $%.2f（%d 折）。",
			promoButton:    "🚀 立即充值，享 %d 折",
		}
	}
	switch strings.Split(locale, "-")[0] {
	case "ja":
		return trialLimitNotificationCopy{"⚠️ GPT 体験クレジットがまもなくなくなります", "💳 継続して利用するにはウォレットにチャージしてください。", "💰 体験クレジットは公式価格、ウォレット残高は割引価格で課金されます。例：GPT-5.6 は 97% お得です。", "🔑 チャージ後も、再設定なしで現在の API キーをそのまま利用できます。", "🤖 1つの API キーで GPT、Claude、Gemini、Kimi、GLM、DeepSeek など主要モデルを利用できます。", "🚀 チャージして利用を続ける", "🎁 初回チャージ限定 %d%% OFF：$%d をチャージ、支払いは $%.2f。", "🚀 %d%% OFF でチャージ"}
	case "pt":
		return trialLimitNotificationCopy{"⚠️ Seu crédito de teste GPT está quase esgotado", "💳 Adicione saldo à carteira para continuar usando a API.", "💰 O crédito de teste usa os preços oficiais; o saldo da carteira usa preços com desconto. Por exemplo, o GPT-5.6 pode economizar 97%.", "🔑 Depois de adicionar saldo, continue usando sua API Key atual sem reconfigurar.", "🤖 Uma única API Key dá acesso a GPT, Claude, Gemini, Kimi, GLM, DeepSeek e outros modelos principais.", "🚀 Adicionar saldo e continuar", "🎁 Oferta limitada na primeira recarga: adicione $%d e pague apenas $%.2f (%d%% OFF).", "🚀 Recarregar com %d%% OFF"}
	case "de":
		return trialLimitNotificationCopy{"⚠️ Dein GPT-Testguthaben ist fast aufgebraucht", "💳 Lade dein Wallet auf, um die API weiter zu nutzen.", "💰 Testguthaben wird zu offiziellen Preisen abgerechnet; Wallet-Guthaben wird zum Rabattpreis abgerechnet. Bei GPT-5.6 sparst du zum Beispiel 97%.", "🔑 Nach dem Aufladen kannst du deinen aktuellen API-Key ohne neue Konfiguration weiterverwenden.", "🤖 Ein API-Key bietet Zugriff auf GPT, Claude, Gemini, Kimi, GLM, DeepSeek und weitere führende Modelle.", "🚀 Aufladen und weitermachen", "🎁 Begrenztes Erstaufladeangebot: $%d aufladen und nur $%.2f zahlen (%d%% Rabatt).", "🚀 Mit %d%% Rabatt aufladen"}
	case "fr":
		return trialLimitNotificationCopy{"⚠️ Votre crédit d'essai GPT est presque épuisé", "💳 Ajoutez du solde au portefeuille pour continuer à utiliser l'API.", "💰 Le crédit d'essai est facturé au prix officiel ; le solde du portefeuille bénéficie de tarifs réduits. GPT-5.6 permet par exemple d'économiser 97%.", "🔑 Après le rechargement, continuez à utiliser votre clé API actuelle sans nouvelle configuration.", "🤖 Une seule clé API donne accès à GPT, Claude, Gemini, Kimi, GLM, DeepSeek et d'autres modèles majeurs.", "🚀 Recharger et continuer", "🎁 Offre limitée de première recharge : ajoutez $%d et payez seulement $%.2f (%d%% de réduction).", "🚀 Recharger avec %d%% de réduction"}
	case "tr":
		return trialLimitNotificationCopy{"⚠️ GPT deneme krediniz tükenmek üzere", "💳 API'yi kullanmaya devam etmek için cüzdanınıza bakiye ekleyin.", "💰 Deneme kredisi resmi fiyatlarla, cüzdan bakiyesi indirimli fiyatlarla ücretlendirilir. Örneğin GPT-5.6 ile 97% tasarruf edebilirsiniz.", "🔑 Bakiye ekledikten sonra mevcut API anahtarınızı yeniden yapılandırmadan kullanmaya devam edin.", "🤖 Tek bir API anahtarıyla GPT, Claude, Gemini, Kimi, GLM, DeepSeek ve diğer önde gelen modelleri kullanabilirsiniz.", "🚀 Bakiye ekle ve devam et", "🎁 İlk yüklemeye özel sınırlı teklif: $%d yükleyin, yalnızca $%.2f ödeyin (%d%% indirim).", "🚀 %d%% indirimle yükle"}
	case "it":
		return trialLimitNotificationCopy{"⚠️ Il credito di prova GPT è quasi esaurito", "💳 Aggiungi fondi al portafoglio per continuare a usare l'API.", "💰 Il credito di prova usa i prezzi ufficiali; il saldo del portafoglio usa prezzi scontati. Con GPT-5.6 puoi risparmiare, ad esempio, il 97%.", "🔑 Dopo la ricarica puoi continuare a usare la chiave API attuale senza riconfigurarla.", "🤖 Una sola chiave API ti dà accesso a GPT, Claude, Gemini, Kimi, GLM, DeepSeek e altri modelli principali.", "🚀 Ricarica e continua", "🎁 Offerta limitata sulla prima ricarica: aggiungi $%d e paga solo $%.2f (%d%% di sconto).", "🚀 Ricarica con il %d%% di sconto"}
	case "pl":
		return trialLimitNotificationCopy{"⚠️ Twój kredyt testowy GPT jest prawie wyczerpany", "💳 Doładuj portfel, aby dalej korzystać z API.", "💰 Kredyt testowy jest rozliczany według oficjalnych cen, a saldo portfela według cen z rabatem. Na przykład GPT-5.6 może kosztować 97% mniej.", "🔑 Po doładowaniu możesz nadal używać obecnego klucza API bez ponownej konfiguracji.", "🤖 Jeden klucz API zapewnia dostęp do GPT, Claude, Gemini, Kimi, GLM, DeepSeek i innych najważniejszych modeli.", "🚀 Doładuj i korzystaj dalej", "🎁 Ograniczona oferta pierwszego doładowania: doładuj $%d i zapłać tylko $%.2f (rabat %d%%).", "🚀 Doładuj z rabatem %d%%"}
	case "id":
		return trialLimitNotificationCopy{"⚠️ Kredit uji coba GPT Anda hampir habis", "💳 Tambahkan saldo dompet untuk terus menggunakan API.", "💰 Kredit uji coba dikenai harga resmi; saldo dompet dikenai harga diskon. Misalnya, GPT-5.6 dapat menghemat 97%.", "🔑 Setelah mengisi saldo, gunakan API Key saat ini tanpa konfigurasi ulang.", "🤖 Satu API Key memberi akses ke GPT, Claude, Gemini, Kimi, GLM, DeepSeek, dan model utama lainnya.", "🚀 Isi saldo dan lanjutkan", "🎁 Promo isi saldo pertama terbatas: isi $%d dan bayar hanya $%.2f (diskon %d%%).", "🚀 Isi saldo dengan diskon %d%%"}
	case "ko":
		return trialLimitNotificationCopy{"⚠️ GPT 체험 크레딧이 곧 소진됩니다", "💳 API를 계속 사용하려면 지갑 잔액을 충전하세요.", "💰 체험 크레딧은 공식 가격으로, 지갑 잔액은 할인 가격으로 청구됩니다. 예를 들어 GPT-5.6은 97% 절약할 수 있습니다.", "🔑 충전 후에는 다시 설정하지 않고 현재 API Key를 계속 사용할 수 있습니다.", "🤖 하나의 API Key로 GPT, Claude, Gemini, Kimi, GLM, DeepSeek 등 주요 모델을 사용할 수 있습니다.", "🚀 충전하고 계속 사용", "🎁 첫 충전 한정 프로모션: $%d 충전 시 $%.2f만 결제 (%d%% 할인).", "🚀 %d%% 할인으로 충전"}
	case "es":
		return trialLimitNotificationCopy{"⚠️ Tu crédito de prueba de GPT está casi agotado", "💳 Añade saldo a la cartera para seguir usando la API.", "💰 El crédito de prueba se cobra al precio oficial; el saldo de la cartera usa precios con descuento. Por ejemplo, GPT-5.6 puede ahorrar un 97%.", "🔑 Después de recargar, sigue usando tu API Key actual sin volver a configurarla.", "🤖 Una sola API Key permite usar GPT, Claude, Gemini, Kimi, GLM, DeepSeek y otros modelos principales.", "🚀 Recargar y continuar", "🎁 Oferta limitada para la primera recarga: añade $%d y paga solo $%.2f (%d%% de descuento).", "🚀 Recargar con %d%% de descuento"}
	case "ru":
		return trialLimitNotificationCopy{"⚠️ Ваш пробный кредит GPT почти израсходован", "💳 Пополните кошелёк, чтобы продолжить пользоваться API.", "💰 Пробный кредит рассчитывается по официальным ценам, а баланс кошелька — со скидкой. Например, GPT-5.6 позволяет сэкономить 97%.", "🔑 После пополнения можно продолжить использовать текущий API Key без повторной настройки.", "🤖 Один API Key открывает доступ к GPT, Claude, Gemini, Kimi, GLM, DeepSeek и другим основным моделям.", "🚀 Пополнить и продолжить", "🎁 Ограниченное предложение для первого пополнения: внесите $%d и заплатите только $%.2f (скидка %d%%).", "🚀 Пополнить со скидкой %d%%"}
	case "vi":
		return trialLimitNotificationCopy{"⚠️ Tín dụng dùng thử GPT của bạn sắp hết", "💳 Hãy nạp số dư ví để tiếp tục sử dụng API.", "💰 Tín dụng dùng thử tính theo giá chính thức; số dư ví tính theo giá chiết khấu. Ví dụ, GPT-5.6 có thể tiết kiệm 97%.", "🔑 Sau khi nạp tiền, bạn có thể tiếp tục dùng API Key hiện tại mà không cần cấu hình lại.", "🤖 Một API Key cho phép bạn dùng GPT, Claude, Gemini, Kimi, GLM, DeepSeek và các mô hình chính khác.", "🚀 Nạp tiền và tiếp tục", "🎁 Ưu đãi nạp lần đầu có hạn: nạp $%d, chỉ thanh toán $%.2f (giảm %d%%).", "🚀 Nạp với ưu đãi %d%%"}
	default:
		return trialLimitNotificationCopy{"⚠️ Your GPT trial credit is almost used up", "💳 Add wallet credit to keep using the API.", "💰 Trial credit is charged at official prices; wallet credit uses discounted rates, e.g. GPT-5.6 can save 97%.", "🔑 After topping up, keep using your current API key without reconfiguration.", "🤖 One API key gives you access to GPT, Claude, Gemini, Kimi, GLM, DeepSeek and other leading models.", "🚀 Top up and continue", "🎁 Limited first top-up: add $%d and pay only $%.2f (%d%% off).", "🚀 Top up with %d%% off"}
	}
}

func renderGPTTrialLimitNotification(variant, language string) (string, string) {
	copy := trialLimitNotificationCopyFor(language)
	text := copy.title + "\n\n" + copy.continueText + "\n\n" + copy.pricingText + "\n\n" + copy.keyText + "\n\n" + copy.modelsText
	if variant != model.TrialLimitNotificationFirstTopupPromo {
		return text, copy.standardButton
	}
	discountPercent := int(common.FirstTopupPromoDiscount*100 + 0.5)
	payAmount := float64(common.FirstTopupPromoAmount) * common.FirstTopupPromoDiscount
	text += fmt.Sprintf("\n\n"+copy.promoLine, common.FirstTopupPromoAmount, payAmount, discountPercent)
	return text, fmt.Sprintf(copy.promoButton, discountPercent)
}

func sendTrialLimitTelegramMessage(chatID, text, button, targetURL string) error {
	if strings.TrimSpace(common.TelegramBotToken) == "" {
		return errors.New("Telegram bot is not configured")
	}
	payload, err := common.Marshal(map[string]any{
		"chat_id":      chatID,
		"text":         text,
		"reply_markup": map[string]any{"inline_keyboard": [][]map[string]string{{{"text": button, "url": targetURL}}}},
	})
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	endpoint := "https://api.telegram.org/bot" + common.TelegramBotToken + "/sendMessage"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := trialLimitNotificationHTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("Telegram sendMessage returned HTTP %d", resp.StatusCode)
	}
	var result struct {
		OK          bool   `json:"ok"`
		Description string `json:"description"`
	}
	if err := common.DecodeJson(resp.Body, &result); err != nil {
		return err
	}
	if !result.OK {
		return fmt.Errorf("Telegram sendMessage failed: %s", result.Description)
	}
	return nil
}

func sendTrialLimitDiscordMessage(recipientID, text, button, targetURL string) error {
	botToken := strings.TrimSpace(os.Getenv("DISCORD_COMMUNITY_BOT_TOKEN"))
	if botToken == "" {
		return errors.New("Discord bot is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	createPayload, err := common.Marshal(map[string]string{"recipient_id": recipientID})
	if err != nil {
		return err
	}
	channelID, err := doDiscordTrialLimitRequest(ctx, botToken, http.MethodPost, "https://discord.com/api/v10/users/@me/channels", createPayload, true)
	if err != nil {
		return err
	}
	messagePayload, err := common.Marshal(map[string]any{
		"content": text,
		"components": []map[string]any{{
			"type":       1,
			"components": []map[string]any{{"type": 2, "style": 5, "label": button, "url": targetURL}},
		}},
	})
	if err != nil {
		return err
	}
	_, err = doDiscordTrialLimitRequest(ctx, botToken, http.MethodPost, "https://discord.com/api/v10/channels/"+channelID+"/messages", messagePayload, false)
	return err
}

func doDiscordTrialLimitRequest(ctx context.Context, botToken, method, endpoint string, payload []byte, needsChannelID bool) (string, error) {
	req, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bot "+botToken)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := trialLimitNotificationHTTPClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return "", fmt.Errorf("Discord API returned HTTP %d", resp.StatusCode)
	}
	if !needsChannelID {
		return "", nil
	}
	var result struct {
		ID string `json:"id"`
	}
	if err := common.DecodeJson(resp.Body, &result); err != nil {
		return "", err
	}
	if _, ok := parseNumericSocialID(result.ID); !ok {
		return "", errors.New("Discord returned an invalid DM channel")
	}
	return result.ID, nil
}
