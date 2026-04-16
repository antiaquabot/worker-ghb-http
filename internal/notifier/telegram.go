package notifier

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Notifier sends messages via Telegram Bot API.
type Notifier = Telegram

// Telegram sends notifications via Telegram Bot API.
type Telegram struct {
	enabled  bool
	botToken string
	chatID   string
	client   *http.Client
}

// New creates a new Telegram notifier.
func New(enabled bool, botToken, chatID string) *Telegram {
	return &Telegram{
		enabled:  enabled,
		botToken: botToken,
		chatID:   chatID,
		client:   &http.Client{Timeout: 15 * time.Second},
	}
}

// IsEnabled returns true if Telegram notifications are enabled.
func (t *Telegram) IsEnabled() bool {
	return t.enabled
}

// Send sends a text message (HTML parse_mode) to the configured chat.
func (t *Telegram) Send(ctx context.Context, text string) error {
	if t.botToken == "" || t.chatID == "" {
		return nil // skip if not configured
	}
	apiURL := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", t.botToken)

	payload := map[string]any{
		"chat_id":                  t.chatID,
		"text":                     text,
		"parse_mode":               "HTML",
		"disable_web_page_preview": true,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := t.client.Do(req)
	if err != nil {
		return fmt.Errorf("send: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("telegram error %d: %s", resp.StatusCode, string(respBody))
	}
	return nil
}

// FormatRegistrationOpened formats the REGISTRATION_OPENED notification.
func (t *Telegram) FormatRegistrationOpened(externalID string, data map[string]any) string {
	title, _ := data["title"].(string)
	regURL, _ := data["registration_url"].(string)

	msg := "🚀 <b>Открылась регистрация!</b>\n\n"
	if title != "" {
		msg += fmt.Sprintf("📦 %s\n", title)
	} else {
		msg += fmt.Sprintf("📦 Объект: %s\n", externalID)
	}
	if regURL != "" {
		msg += fmt.Sprintf("🔗 <a href=\"%s\">Зарегистрироваться</a>\n", regURL)
	}
	msg += fmt.Sprintf("\n🕐 %s", time.Now().Format("02.01.2006 15:04:05"))
	return msg
}

// FormatSMSCodeRequest formats the SMS code request message with an expiry deadline.
func (t *Telegram) FormatSMSCodeRequest(deadline time.Time) string {
	return fmt.Sprintf(
		"📲 На ваш номер телефона отправлен код подтверждения.\n"+
			"Введите код до [%s] — иначе регистрация завершится с ошибкой.\n"+
			"Отправьте код ответным сообщением.",
		deadline.Format("02.01.2006 15:04:05"),
	)
}

// FormatRegistrationClosed formats the REGISTRATION_CLOSED notification.
func (t *Telegram) FormatRegistrationClosed(externalID string) string {
	return fmt.Sprintf(
		"🔒 <b>Регистрация закрыта</b>\n\n📦 Объект: %s\n🕐 %s",
		externalID, time.Now().Format("02.01.2006 15:04:05"),
	)
}

// FormatRegistrationSuccess formats the successful auto-registration notification.
func (t *Telegram) FormatRegistrationSuccess(externalID string) string {
	return fmt.Sprintf(
		"✅ <b>Авторегистрация выполнена!</b>\n\n📦 Объект: %s\n🕐 %s",
		externalID, time.Now().Format("02.01.2006 15:04:05"),
	)
}

// FormatRegistrationError formats the auto-registration error notification.
func (t *Telegram) FormatRegistrationError(externalID string, err error) string {
	return fmt.Sprintf(
		"❌ <b>Ошибка авторегистрации</b>\n\n📦 Объект: %s\n⚠️ %s\n🕐 %s",
		externalID, err.Error(), time.Now().Format("02.01.2006 15:04:05"),
	)
}

// FormatServiceUnavailable formats the service unavailable notification.
func (t *Telegram) FormatServiceUnavailable(minutes int) string {
	return fmt.Sprintf("⚠️ Сервис мониторинга недоступен более %d мин.", minutes)
}

func (t *Telegram) WaitForCode(ctx context.Context, requestMessageID int) (string, error) {
	if t.botToken == "" || t.chatID == "" {
		return "", nil
	}

	apiURL := fmt.Sprintf("https://api.telegram.org/bot%s/getUpdates", t.botToken)

	// update wraps the Telegram Update object.
	// UpdateID is the sequence number used as the getUpdates offset; it is
	// completely separate from MessageID and must not be confused with it.
	type update struct {
		UpdateID int `json:"update_id"` // used for offset advancement
		Message  struct {
			Text string `json:"text"`
			Chat struct {
				ID int64 `json:"id"`
			} `json:"chat"`
			MessageID int `json:"message_id"`
		} `json:"message"`
	}

	// lastUpdateID tracks the Telegram update_id (not message_id) so that
	// each getUpdates call fetches only new updates via offset=lastUpdateID+1.
	var lastUpdateID int
	for {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		default:
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
		if err != nil {
			continue
		}

		q := req.URL.Query()
		q.Set("offset", fmt.Sprintf("%d", lastUpdateID+1))
		q.Set("timeout", "30")
		req.URL.RawQuery = q.Encode()

		resp, err := t.client.Do(req)
		if err != nil {
			time.Sleep(time.Second)
			continue
		}

		var result struct {
			OK     bool     `json:"ok"`
			Error  string   `json:"error_string,omitempty"`
			Result []update `json:"result"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			resp.Body.Close()
			time.Sleep(time.Second)
			continue
		}
		resp.Body.Close()

		if !result.OK {
			time.Sleep(time.Second)
			continue
		}

		for _, u := range result.Result {
			// Always advance the offset using update_id, not message_id.
			if u.UpdateID > lastUpdateID {
				lastUpdateID = u.UpdateID
			}
			if u.Message.Chat.ID != 0 && u.Message.Text != "" {
				if requestMessageID > 0 {
					if u.Message.MessageID > requestMessageID {
						return u.Message.Text, nil
					}
				} else {
					return u.Message.Text, nil
				}
			}
		}
	}
}
