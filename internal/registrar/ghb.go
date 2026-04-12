package registrar

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/stroi-homes/worker-ghb-http/internal/config"
)

const (
	regBaseURL     = "https://reg.ghb.by"
	defaultTimeout = 15 * time.Second
	smsWaitTimeout = 3 * time.Minute
)

var (
	megaAlertRe = regexp.MustCompile(`(?i)<[^>]*class="[^"]*megaalert-content[^"]*"[^>]*>([\s\S]*?)</[^>]+>`)
	htmlTagRe   = regexp.MustCompile(`<[^>]+>`)
	spaceRe     = regexp.MustCompile(`\s+`)
)

// extractError parses the .megaalert-content block from HTML and returns its
// trimmed inner text, or "" if not found.
func extractError(body string) string {
	m := megaAlertRe.FindStringSubmatch(body)
	if m == nil {
		return ""
	}
	text := htmlTagRe.ReplaceAllString(m[1], " ")
	text = strings.TrimSpace(spaceRe.ReplaceAllString(text, " "))
	text = strings.ReplaceAll(text, "•", "")
	return strings.TrimSpace(text)
}

// Registrar performs auto-registration on the developer's website.
type Registrar interface {
	// Register starts a registration flow using the registration URL from the event.
	// regURL is the full registration URL (e.g., "https://reg.ghb.by/register/?id=12345").
	// smsCodeFn blocks until the user provides the SMS code (via Telegram or stdin).
	Register(ctx context.Context, objectID string, regURL string, personalData config.PersonalData, smsCodeFn SMSCodeFunc) error
}

// SMSCodeFunc is called when the server is waiting for an SMS confirmation code.
// It should block until the user provides the code.
type SMSCodeFunc func(ctx context.Context) (string, error)

// GHBRegistrar implements Registrar for GHB via direct HTTP requests.
//
// Registration flow (4 steps):
//  1. GET /register/?id=<objectID>  — acquire PHPSESSID session cookie
//  2. POST /register/?id=<objectID> — submit personal data (act=reg_user); server sends SMS
//  3. [wait for SMS code via smsCodeFn]
//  4. POST /register/?id=<objectID> — submit SMS code (act=conf_user); expect 302 = success
type GHBRegistrar struct {
	client *http.Client
}

func NewGHBRegistrar() *GHBRegistrar {
	jar, _ := cookiejar.New(nil)
	return &GHBRegistrar{
		client: &http.Client{
			Timeout: defaultTimeout,
			Jar:     jar,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if len(via) >= 5 {
					return fmt.Errorf("too many redirects")
				}
				return nil
			},
		},
	}
}

// Register implements the GHB 4-step online registration flow.
// regURL is the full registration URL (e.g., "https://reg.ghb.by/register/?id=12345").
func (r *GHBRegistrar) Register(
	ctx context.Context,
	objectID string,
	regURL string,
	pd config.PersonalData,
	smsCodeFn SMSCodeFunc,
) error {
	// -----------------------------------------------------------------------
	// Step 1: GET — acquire session cookie
	// -----------------------------------------------------------------------
	log.Printf("[ghb-registrar] step 1: GET %s", regURL)
	resp1, err := r.get(ctx, regURL, "")
	if err != nil {
		return fmt.Errorf("step 1 GET: %w", err)
	}
	resp1.Body.Close()
	if resp1.StatusCode != http.StatusOK {
		return fmt.Errorf("step 1: unexpected status %d", resp1.StatusCode)
	}
	log.Printf("[ghb-registrar] step 1 OK — session cookie obtained")

	// -----------------------------------------------------------------------
	// Step 2: POST — submit personal data
	// -----------------------------------------------------------------------
	last, first, middle := pd.Parts()
	if last == "" || first == "" {
		return fmt.Errorf("personal_data: last_name and first_name are required")
	}
	phone := pd.PhoneDigits()
	if len(phone) != 9 {
		return fmt.Errorf("personal_data: phone must have 9 digits after stripping prefix, got %q", phone)
	}

	formData := url.Values{
		"act":        {"reg_user"},
		"lastname":   {last},
		"firstname":  {first},
		"middlename": {middle},
		"phone":      {phone},
		"consent":    {"1"},
	}
	log.Printf("[ghb-registrar] step 2: POST personal data for object %s", objectID)
	resp2, err := r.post(ctx, regURL, regURL, formData)
	if err != nil {
		return fmt.Errorf("step 2 POST: %w", err)
	}
	body2, _ := io.ReadAll(resp2.Body)
	resp2.Body.Close()

	body2str := string(body2)
	if isAlreadyRegistered(body2str) {
		return fmt.Errorf("already registered for object %s", objectID)
	}
	// success = 200 or 302 (redirected after form submission)
	if resp2.StatusCode != http.StatusOK && resp2.StatusCode != http.StatusFound {
		return fmt.Errorf("step 2: unexpected status %d", resp2.StatusCode)
	}
	log.Printf("[ghb-registrar] step 2 OK — server is sending SMS to ***%s", phone[6:])

	// -----------------------------------------------------------------------
	// Step 3: GET — verify SMS form is present
	// -----------------------------------------------------------------------
	log.Printf("[ghb-registrar] step 3: GET — verifying SMS form")
	resp3, err := r.get(ctx, regURL, regURL)
	if err != nil {
		return fmt.Errorf("step 3 GET: %w", err)
	}
	body3, _ := io.ReadAll(resp3.Body)
	resp3.Body.Close()

	body3str := string(body3)
	if isSuccess(body3str) {
		log.Printf("[ghb-registrar] step 3: registration already completed (no SMS needed)")
		return nil
	}
	if !hasSMSForm(body3str) {
		return fmt.Errorf("step 3: SMS code form not found — registration may have failed. Check that the object is still accepting registrations")
	}
	log.Printf("[ghb-registrar] step 3 OK — SMS form confirmed, waiting for user to provide code")

	// -----------------------------------------------------------------------
	// Step 3.5: Wait for SMS code
	// -----------------------------------------------------------------------
	smsCtx, smsCancel := context.WithTimeout(ctx, smsWaitTimeout)
	defer smsCancel()

	code, err := smsCodeFn(smsCtx)
	if err != nil {
		return fmt.Errorf("waiting for SMS code: %w", err)
	}
	code = strings.TrimSpace(code)
	if code == "" {
		return fmt.Errorf("empty SMS code received")
	}
	log.Printf("[ghb-registrar] SMS code received, submitting")

	// -----------------------------------------------------------------------
	// Step 4: POST — submit SMS confirmation code
	// -----------------------------------------------------------------------
	confData := url.Values{
		"act":      {"conf_user"},
		"sms_code": {code},
	}
	resp4, err := r.post(ctx, regURL, regURL, confData)
	if err != nil {
		return fmt.Errorf("step 4 POST: %w", err)
	}
	body4, _ := io.ReadAll(resp4.Body)
	resp4.Body.Close()

	body4str := string(body4)
	// Success: 302 Found (redirect) or success message in HTML
	if resp4.StatusCode == http.StatusFound || isSuccess(body4str) {
		log.Printf("[ghb-registrar] registration completed successfully for object %s", objectID)
		return nil
	}
	// Wrong code
	lower4 := strings.ToLower(body4str)
	if strings.Contains(lower4, "неверн") || strings.Contains(lower4, "incorrect") {
		return fmt.Errorf("step 4: SMS code is incorrect")
	}

	return fmt.Errorf("step 4: unexpected status %d — registration may have failed", resp4.StatusCode)
}

// ---------------------------------------------------------------------------
// HTTP helpers
// ---------------------------------------------------------------------------

func (r *GHBRegistrar) get(ctx context.Context, rawURL, referer string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	r.setCommonHeaders(req, referer)
	return r.client.Do(req)
}

func (r *GHBRegistrar) post(ctx context.Context, rawURL, referer string, form url.Values) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, rawURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	r.setCommonHeaders(req, referer)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if referer != "" {
		req.Header.Set("Origin", originOf(referer))
	}
	return r.client.Do(req)
}

func (r *GHBRegistrar) setCommonHeaders(req *http.Request, referer string) {
	req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "ru-RU,ru;q=0.9,en-US;q=0.8,en;q=0.7")
	if referer != "" {
		req.Header.Set("Referer", referer)
	}
}

// ---------------------------------------------------------------------------
// HTML detection helpers
// ---------------------------------------------------------------------------

func hasSMSForm(body string) bool {
	lower := strings.ToLower(body)
	return strings.Contains(lower, "sms_code") ||
		strings.Contains(lower, "смс-код") ||
		strings.Contains(lower, "введите код")
}

func isSuccess(body string) bool {
	lower := strings.ToLower(body)
	return strings.Contains(lower, "регистрация завершена") ||
		strings.Contains(lower, "зарегистрированы") ||
		strings.Contains(lower, "успешно зарегистрирован")
}

func isAlreadyRegistered(body string) bool {
	lower := strings.ToLower(body)
	return strings.Contains(lower, "уже зарегистрирован") ||
		strings.Contains(lower, "already registered")
}

func originOf(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	return u.Scheme + "://" + u.Host
}
