package customerbot

// Minimal Telegram Bot API types — only the fields this module consumes. The
// bot trusts only updates fetched from the official API over its own token.

type User struct {
	ID           int64  `json:"id"`
	IsBot        bool   `json:"is_bot"`
	Username     string `json:"username"`
	FirstName    string `json:"first_name"`
	LanguageCode string `json:"language_code"`
}

type Chat struct {
	ID   int64  `json:"id"`
	Type string `json:"type"`
}

type SuccessfulPayment struct {
	Currency                string `json:"currency"`
	TotalAmount             int64  `json:"total_amount"`
	InvoicePayload          string `json:"invoice_payload"`
	TelegramPaymentChargeID string `json:"telegram_payment_charge_id"`
	ProviderPaymentChargeID string `json:"provider_payment_charge_id"`
}

type Message struct {
	MessageID         int64              `json:"message_id"`
	From              *User              `json:"from"`
	Chat              Chat               `json:"chat"`
	Text              string             `json:"text"`
	SuccessfulPayment *SuccessfulPayment `json:"successful_payment"`
}

type CallbackQuery struct {
	ID      string   `json:"id"`
	From    User     `json:"from"`
	Data    string   `json:"data"`
	Message *Message `json:"message"`
}

type PreCheckoutQuery struct {
	ID             string `json:"id"`
	From           User   `json:"from"`
	Currency       string `json:"currency"`
	TotalAmount    int64  `json:"total_amount"`
	InvoicePayload string `json:"invoice_payload"`
}

type Update struct {
	UpdateID         int64             `json:"update_id"`
	Message          *Message          `json:"message"`
	CallbackQuery    *CallbackQuery    `json:"callback_query"`
	PreCheckoutQuery *PreCheckoutQuery `json:"pre_checkout_query"`
}

// Inline keyboard.
type InlineButton struct {
	Text         string `json:"text"`
	CallbackData string `json:"callback_data,omitempty"`
	URL          string `json:"url,omitempty"`
	Pay          bool   `json:"pay,omitempty"`
}

type Keyboard struct {
	InlineKeyboard [][]InlineButton `json:"inline_keyboard"`
}
