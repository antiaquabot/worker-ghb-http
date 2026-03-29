package notifier

import "context"

// Telegram sends notifications via Telegram Bot API.
type Telegram struct {
	token  string
	chatID string
}

// New creates a new Telegram notifier.
func New(token, chatID string) *Telegram {
	return &Telegram{token: token, chatID: chatID}
}

// Send sends a text message to the configured chat.
func (t *Telegram) Send(ctx context.Context, msg string) error {
	_ = ctx
	_ = msg
	return nil
}

// FormatRegistrationOpened formats a notification for a registration-opened event.
func (t *Telegram) FormatRegistrationOpened(externalID string, data map[string]any) string {
	_ = externalID
	_ = data
	return ""
}
