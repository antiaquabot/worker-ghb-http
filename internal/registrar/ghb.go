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

const tempErrorText = "Попробуйте позже"

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
	Register(
		ctx context.Context,
		objectID string,
		regURL string,
		pd config.PersonalData,
		cfg config.RegistrationConfig,
		smsCodeFn SMSCodeFunc,
	) error
}

// SMSCodeFunc is called when the server is waiting for an SMS confirmation code.
// It should block until the user provides the code.
type SMSCodeFunc func(ctx context.Context) (string, error)

// GHBRegistrar implements Registrar for GHB via direct HTTP requests.
//
// Registration flow (5 steps):
//  1. GET /register/?id=<objectID>          — acquire PHPSESSID session cookie
//  2. POST /register/?id=<objectID>         — submit personal data (act=reg_user); server sends SMS
//  3. GET /register/?id=<objectID>          — verify SMS form is present
//     3.5. [wait for SMS code via smsCodeFn]
//  4. POST /register/?id=<objectID>         — submit SMS code (act=conf_user); expect 302 = success
//  5. GET /register/?id=<objectID>          — verify "Регистрация завершена" on page
//
// Two http.Client instances share the same cookiejar.Jar:
//   - getClient  follows redirects normally (steps 1, 3, 5).
//   - postClient returns raw redirect responses (steps 2, 4) so the caller can
//     detect 302 (success) and handle 301 (re-POST to canonical URL).
type GHBRegistrar struct {
	getClient  *http.Client
	postClient *http.Client
}

func NewGHBRegistrar() *GHBRegistrar {
	jar, _ := cookiejar.New(nil)
	return &GHBRegistrar{
		getClient: &http.Client{
			Jar: jar,
		},
		postClient: &http.Client{
			Jar: jar,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
}

// Register attempts the 5-step GHB registration flow up to 2 times.
func (r *GHBRegistrar) Register(
	ctx context.Context,
	objectID string,
	regURL string,
	pd config.PersonalData,
	cfg config.RegistrationConfig,
	smsCodeFn SMSCodeFunc,
) error {
	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		if attempt > 0 {
			log.Printf("[ghb-registrar] attempt %d for object %s", attempt+1, objectID)
			time.Sleep(500 * time.Millisecond)
		}
		if err := r.doRegister(ctx, objectID, regURL, pd, cfg, smsCodeFn); err != nil {
			lastErr = err
			log.Printf("[ghb-registrar] attempt %d failed: %v", attempt+1, err)
			continue
		}
		return nil
	}
	return lastErr
}

func (r *GHBRegistrar) doRegister(
	ctx context.Context,
	objectID string,
	regURL string,
	pd config.PersonalData,
	cfg config.RegistrationConfig,
	smsCodeFn SMSCodeFunc,
) error {
	retryTimeout := time.Duration(cfg.RetryTimeoutMs) * time.Millisecond
	retryInt := time.Duration(cfg.RetryIntervalMs) * time.Millisecond
	httpTimeout := time.Duration(cfg.RegisterTimeoutMs) * time.Millisecond

	// -----------------------------------------------------------------------
	// Step 1: GET — acquire session cookie
	// -----------------------------------------------------------------------
	log.Printf("[ghb-registrar] step 1: GET %s", regURL)
	reqCtx1, cancel1 := context.WithTimeout(ctx, httpTimeout)
	resp1, err := r.get(reqCtx1, regURL, "")
	cancel1()
	if err != nil {
		return fmt.Errorf("step 1 GET: %w", err)
	}
	body1, _ := io.ReadAll(resp1.Body)
	resp1.Body.Close()
	if errText := extractError(string(body1)); errText != "" {
		return fmt.Errorf("step 1: %s", errText)
	}
	if resp1.StatusCode != http.StatusOK {
		return fmt.Errorf("step 1: unexpected status %d", resp1.StatusCode)
	}
	// Capture final URL after any GET redirects (HTTP→HTTPS, www→no-www, etc.)
	currentURL := resp1.Request.URL.String()
	log.Printf("[ghb-registrar] step 1 OK — session cookie obtained, final URL: %s", currentURL)

	// -----------------------------------------------------------------------
	// Step 2: POST — submit personal data; server sends SMS on success (302)
	// -----------------------------------------------------------------------
	last, first, middle := pd.Parts()
	if last == "" || first == "" {
		return fmt.Errorf("personal_data: last_name and first_name are required")
	}
	phone := pd.PhoneDigits()
	if len(phone) != 9 {
		return fmt.Errorf("personal_data: phone must have 9 digits, got %q", phone)
	}
	formData := url.Values{
		"act":        {"reg_user"},
		"lastname":   {last},
		"firstname":  {first},
		"middlename": {middle},
		"phone":      {phone},
		"consent":    {"1"},
	}
	postURL := currentURL
	retryDeadline2 := time.Now().Add(retryTimeout)
	log.Printf("[ghb-registrar] step 2: POST personal data for object %s", objectID)
step2:
	for {
		reqCtx2, cancel2 := context.WithTimeout(ctx, httpTimeout)
		resp2, err := r.post(reqCtx2, postURL, postURL, formData)
		cancel2()
		if err != nil {
			return fmt.Errorf("step 2 POST: %w", err)
		}
		switch resp2.StatusCode {
		case http.StatusFound: // 302 — SMS sent
			resp2.Body.Close()
			log.Printf("[ghb-registrar] step 2 OK — server sent SMS (302)")
			break step2
		case http.StatusMovedPermanently: // 301 — re-POST to canonical URL
			location := resp2.Header.Get("Location")
			resp2.Body.Close()
			if location == "" || time.Now().After(retryDeadline2) {
				return fmt.Errorf("step 2: 301 redirect without Location or retry timeout exceeded")
			}
			log.Printf("[ghb-registrar] step 2: 301 → %s, retrying POST", location)
			postURL = location
			time.Sleep(retryInt)
		default:
			body2, _ := io.ReadAll(resp2.Body)
			resp2.Body.Close()
			body2str := string(body2)
			if isAlreadyRegistered(body2str) {
				return fmt.Errorf("already registered for object %s", objectID)
			}
			if errText := extractError(body2str); errText != "" {
				if strings.Contains(errText, tempErrorText) && time.Now().Before(retryDeadline2) {
					log.Printf("[ghb-registrar] step 2: temporary error %q, retrying in %v", errText, retryInt)
					time.Sleep(retryInt)
					continue
				}
				return fmt.Errorf("step 2: %s", errText)
			}
			log.Printf("[ghb-registrar] step 2: status %d, no error found, continuing", resp2.StatusCode)
			break step2
		}
	}

	// -----------------------------------------------------------------------
	// Step 3: GET — verify SMS form is present
	// -----------------------------------------------------------------------
	log.Printf("[ghb-registrar] step 3: GET — verifying SMS form from %s", postURL)
	reqCtx3, cancel3 := context.WithTimeout(ctx, httpTimeout)
	resp3, err := r.get(reqCtx3, postURL, postURL)
	cancel3()
	if err != nil {
		return fmt.Errorf("step 3 GET: %w", err)
	}
	body3, _ := io.ReadAll(resp3.Body)
	resp3.Body.Close()
	body3str := string(body3)
	if errText := extractError(body3str); errText != "" {
		return fmt.Errorf("step 3: %s", errText)
	}
	if isSuccess(body3str) {
		log.Printf("[ghb-registrar] step 3: registration already completed (no SMS needed)")
		return nil
	}
	if !hasSMSForm(body3str) {
		return fmt.Errorf("step 3: SMS code form not found — registration may have failed")
	}
	log.Printf("[ghb-registrar] step 3 OK — SMS form confirmed, waiting for user code")

	// -----------------------------------------------------------------------
	// Step 3.5: Wait for SMS code
	// -----------------------------------------------------------------------
	smsTimeout := time.Duration(cfg.SMSCodeWaitTimeoutS) * time.Second
	smsCtx, smsCancel := context.WithTimeout(ctx, smsTimeout)
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
	retryDeadline4 := time.Now().Add(retryTimeout)
	log.Printf("[ghb-registrar] step 4: POST SMS code")
step4:
	for {
		reqCtx4, cancel4 := context.WithTimeout(ctx, httpTimeout)
		resp4, err := r.post(reqCtx4, postURL, postURL, confData)
		cancel4()
		if err != nil {
			return fmt.Errorf("step 4 POST: %w", err)
		}
		if resp4.StatusCode == http.StatusFound {
			resp4.Body.Close()
			log.Printf("[ghb-registrar] step 4 OK — SMS code accepted (302)")
			break step4
		}
		body4, _ := io.ReadAll(resp4.Body)
		resp4.Body.Close()
		body4str := string(body4)
		if errText := extractError(body4str); errText != "" {
			if strings.Contains(errText, tempErrorText) && time.Now().Before(retryDeadline4) {
				log.Printf("[ghb-registrar] step 4: temporary error %q, retrying", errText)
				time.Sleep(retryInt)
				continue
			}
			return fmt.Errorf("step 4: %s", errText)
		}
		lower4 := strings.ToLower(body4str)
		if strings.Contains(lower4, "неверн") || strings.Contains(lower4, "incorrect") {
			return fmt.Errorf("step 4: SMS code is incorrect")
		}
		log.Printf("[ghb-registrar] step 4: status %d, no error found", resp4.StatusCode)
		break step4
	}

	// -----------------------------------------------------------------------
	// Step 5: GET — verify success page
	// -----------------------------------------------------------------------
	log.Printf("[ghb-registrar] step 5: GET — loading success page from %s", postURL)
	reqCtx5, cancel5 := context.WithTimeout(ctx, httpTimeout)
	resp5, err := r.get(reqCtx5, postURL, postURL)
	cancel5()
	if err != nil {
		return fmt.Errorf("step 5 GET: %w", err)
	}
	body5, _ := io.ReadAll(resp5.Body)
	resp5.Body.Close()
	body5str := string(body5)
	if errText := extractError(body5str); errText != "" {
		return fmt.Errorf("step 5: %s", errText)
	}
	if isSuccess(body5str) {
		log.Printf("[ghb-registrar] registration completed successfully for object %s", objectID)
		return nil
	}
	return fmt.Errorf("step 5: success confirmation not received")
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
	return r.getClient.Do(req)
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
	return r.postClient.Do(req)
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
