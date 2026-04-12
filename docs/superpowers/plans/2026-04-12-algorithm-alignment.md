# Algorithm Alignment + Dual Mode + Release Prep — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Align the Go worker's registration algorithm with the Python reference implementation, complete dual-mode (Telegram / terminal) support, and ship v1.0.0.

**Architecture:** Two `http.Client` instances share a `cookiejar.Jar` — `getClient` follows redirects normally (Steps 1, 3, 5), `postClient` returns raw redirect responses so the caller can handle 301 re-POST and detect 302 success. `RegistrationConfig` carries all configurable timeouts and is passed through `Register()`.

**Tech Stack:** Go 1.22+, `net/http`, `net/http/cookiejar`, `regexp`, `gopkg.in/yaml.v3`

---

## File Map

| File | Action | Responsibility |
|------|--------|----------------|
| `internal/config/config.go` | Modify | Add `RegistrationConfig`, apply defaults in `Load()` |
| `internal/config/config_test.go` | Create | Test `RegistrationConfig` defaults |
| `internal/registrar/ghb.go` | Modify | Two-client struct, `extractError`, full 5-step flow, 2 attempts |
| `internal/registrar/ghb_test.go` | Create | Unit tests for pure HTML-detection functions |
| `internal/notifier/telegram.go` | Modify | Add `FormatSMSCodeRequest(deadline time.Time)` |
| `main.go` | Modify | Pass `cfg.Registration` to `Register()`, deadline in `smsCodeFn`, terminal success log |
| `config.example.yaml` | Modify | Add `registration:` section |
| `README.md` | Modify | Terminal-mode section, updated modes table, `registration:` docs |

---

## Task 1: Add RegistrationConfig to config

**Files:**
- Modify: `internal/config/config.go`
- Create: `internal/config/config_test.go`

- [ ] **Step 1: Add `RegistrationConfig` struct and wire into `Config`**

In `internal/config/config.go`, add the struct and field after the `WatchEntry` type:

```go
type RegistrationConfig struct {
	RetryTimeoutMs      int `yaml:"retry_timeout_ms"`
	RetryIntervalMs     int `yaml:"retry_interval_ms"`
	SMSCodeWaitTimeoutS int `yaml:"sms_code_wait_timeout_s"`
	RegisterTimeoutMs   int `yaml:"register_timeout_ms"`
}
```

Add `Registration RegistrationConfig` field to `Config`:

```go
type Config struct {
	Service      ServiceConfig      `yaml:"service"`
	Telegram     TelegramConfig     `yaml:"telegram"`
	Registration RegistrationConfig `yaml:"registration"`
	PersonalData PersonalData       `yaml:"-"`
	WatchList    []WatchEntry       `yaml:"watch_list"`
}
```

Add `Registration RegistrationConfig` to `rawConfig` as well:

```go
type rawConfig struct {
	Service               ServiceConfig      `yaml:"service"`
	Telegram              TelegramConfig     `yaml:"telegram"`
	Registration          RegistrationConfig `yaml:"registration"`
	PersonalDataEncrypted string             `yaml:"personal_data_encrypted,omitempty"`
	WatchList             []WatchEntry       `yaml:"watch_list"`
}
```

- [ ] **Step 2: Add `applyRegistrationDefaults` and call it in `Load()`**

Add this function (after the `rawConfig` declaration):

```go
func applyRegistrationDefaults(r *RegistrationConfig) {
	if r.RetryTimeoutMs <= 0 {
		r.RetryTimeoutMs = 30000
	}
	if r.RetryIntervalMs <= 0 {
		r.RetryIntervalMs = 500
	}
	if r.SMSCodeWaitTimeoutS <= 0 {
		r.SMSCodeWaitTimeoutS = 180
	}
	if r.RegisterTimeoutMs <= 0 {
		r.RegisterTimeoutMs = 15000
	}
}
```

In `Load()`, copy `raw.Registration` into `cfg` and apply defaults. Replace the existing defaults block:

```go
cfg := &Config{
    Service:      raw.Service,
    Telegram:     raw.Telegram,
    Registration: raw.Registration,
    WatchList:    raw.WatchList,
}

// Set defaults
if cfg.Service.PollIntervalSeconds <= 0 {
    cfg.Service.PollIntervalSeconds = 60
}
applyRegistrationDefaults(&cfg.Registration)
```

- [ ] **Step 3: Write failing tests**

Create `internal/config/config_test.go`:

```go
package config

import "testing"

func TestApplyRegistrationDefaults_AllZero(t *testing.T) {
	r := RegistrationConfig{}
	applyRegistrationDefaults(&r)
	if r.RetryTimeoutMs != 30000 {
		t.Errorf("RetryTimeoutMs = %d, want 30000", r.RetryTimeoutMs)
	}
	if r.RetryIntervalMs != 500 {
		t.Errorf("RetryIntervalMs = %d, want 500", r.RetryIntervalMs)
	}
	if r.SMSCodeWaitTimeoutS != 180 {
		t.Errorf("SMSCodeWaitTimeoutS = %d, want 180", r.SMSCodeWaitTimeoutS)
	}
	if r.RegisterTimeoutMs != 15000 {
		t.Errorf("RegisterTimeoutMs = %d, want 15000", r.RegisterTimeoutMs)
	}
}

func TestApplyRegistrationDefaults_PreserveExisting(t *testing.T) {
	r := RegistrationConfig{
		RetryTimeoutMs:      5000,
		RetryIntervalMs:     200,
		SMSCodeWaitTimeoutS: 60,
		RegisterTimeoutMs:   8000,
	}
	applyRegistrationDefaults(&r)
	if r.RetryTimeoutMs != 5000 {
		t.Errorf("RetryTimeoutMs should not be overwritten, got %d", r.RetryTimeoutMs)
	}
	if r.RetryIntervalMs != 200 {
		t.Errorf("RetryIntervalMs should not be overwritten, got %d", r.RetryIntervalMs)
	}
}
```

- [ ] **Step 4: Run tests — expect FAIL (function not yet exported or not found)**

```bash
cd /Users/alexeyromanovsky/Work/worker-ghb-http
go test ./internal/config/...
```

Expected: FAIL — `applyRegistrationDefaults undefined` (it's unexported; tests are in same package so it's fine — if FAIL is "undefined", you made a typo. If PASS already, that's also fine.)

- [ ] **Step 5: Run tests — expect PASS after Step 2 is done**

```bash
go test ./internal/config/...
```

Expected: `ok  github.com/stroi-homes/worker-ghb-http/internal/config`

- [ ] **Step 6: Verify build**

```bash
go build ./...
```

Expected: no output (success)

- [ ] **Step 7: Commit**

```bash
git add internal/config/config.go internal/config/config_test.go
git commit -m "feat(config): add RegistrationConfig with defaults"
```

---

## Task 2: Add HTML helpers and tests

**Files:**
- Modify: `internal/registrar/ghb.go`
- Create: `internal/registrar/ghb_test.go`

- [ ] **Step 1: Add `extractError` function and regexps to `ghb.go`**

Add imports `"regexp"` to the import block in `internal/registrar/ghb.go`.

Add these package-level variables and function after the `const` block:

```go
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
```

- [ ] **Step 2: Write failing tests**

Create `internal/registrar/ghb_test.go`:

```go
package registrar

import "testing"

func TestExtractError(t *testing.T) {
	tests := []struct {
		name     string
		body     string
		expected string
	}{
		{
			name:     "extracts plain text",
			body:     `<div class="megaalert-content">Ошибка регистрации</div>`,
			expected: "Ошибка регистрации",
		},
		{
			name:     "no megaalert block returns empty",
			body:     `<div class="other">some text</div>`,
			expected: "",
		},
		{
			name:     "strips inner HTML tags",
			body:     `<div class="megaalert-content"><p>Некорректный <b>код</b></p></div>`,
			expected: "Некорректный код",
		},
		{
			name:     "removes bullet character",
			body:     `<div class="megaalert-content">• Попробуйте позже</div>`,
			expected: "Попробуйте позже",
		},
		{
			name:     "normalises whitespace",
			body:     `<div class="megaalert-content">  много   пробелов  </div>`,
			expected: "много пробелов",
		},
		{
			name:     "case-insensitive class match",
			body:     `<DIV CLASS="MegaAlert-Content">Ошибка</DIV>`,
			expected: "Ошибка",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractError(tt.body)
			if got != tt.expected {
				t.Errorf("extractError() = %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestHasSMSForm(t *testing.T) {
	if !hasSMSForm(`<input name="sms_code">`) {
		t.Error("expected hasSMSForm=true for sms_code input")
	}
	if !hasSMSForm(`Введите код из SMS`) {
		t.Error("expected hasSMSForm=true for 'введите код'")
	}
	if hasSMSForm(`<html>some other page</html>`) {
		t.Error("expected hasSMSForm=false for unrelated page")
	}
}

func TestIsSuccess(t *testing.T) {
	if !isSuccess(`<p>Регистрация завершена успешно</p>`) {
		t.Error("expected isSuccess=true")
	}
	if isSuccess(`<p>Ошибка</p>`) {
		t.Error("expected isSuccess=false")
	}
}

func TestIsAlreadyRegistered(t *testing.T) {
	if !isAlreadyRegistered(`Вы уже зарегистрированы на данный объект`) {
		t.Error("expected isAlreadyRegistered=true")
	}
	if isAlreadyRegistered(`Введите SMS-код`) {
		t.Error("expected isAlreadyRegistered=false")
	}
}
```

- [ ] **Step 3: Run tests — expect PASS**

```bash
go test ./internal/registrar/...
```

Expected: `ok  github.com/stroi-homes/worker-ghb-http/internal/registrar`

- [ ] **Step 4: Commit**

```bash
git add internal/registrar/ghb.go internal/registrar/ghb_test.go
git commit -m "feat(registrar): add extractError HTML parser and tests"
```

---

## Task 3: Refactor GHBRegistrar — two HTTP clients, updated interface

**Files:**
- Modify: `internal/registrar/ghb.go`

- [ ] **Step 1: Update `Registrar` interface signature**

Replace the existing `Registrar` interface:

```go
// Registrar performs auto-registration on the developer's website.
type Registrar interface {
	Register(
		ctx context.Context,
		objectID string,
		regURL string,
		pd config.PersonalData,
		cfg config.RegistrationConfig,
		smsCodeFn SMSCodeFunc,
	) error
}
```

- [ ] **Step 2: Replace `GHBRegistrar` struct and constructor**

Replace the existing `GHBRegistrar` struct and `NewGHBRegistrar()`:

```go
// GHBRegistrar implements Registrar for GHB via direct HTTP requests.
//
// Registration flow (5 steps):
//  1. GET /register/?id=<objectID>          — acquire PHPSESSID session cookie
//  2. POST /register/?id=<objectID>         — submit personal data (act=reg_user); server sends SMS
//  3. GET /register/?id=<objectID>          — verify SMS form is present
//  3.5. [wait for SMS code via smsCodeFn]
//  4. POST /register/?id=<objectID>         — submit SMS code (act=conf_user); expect 302 = success
//  5. GET /register/?id=<objectID>          — verify "Регистрация завершена" on page
type GHBRegistrar struct {
	// getClient follows redirects normally — used for GET requests (steps 1, 3, 5).
	getClient *http.Client
	// postClient returns raw redirect responses — used for POST requests (steps 2, 4)
	// so the caller can detect 302 (success) and handle 301 (re-POST to canonical URL).
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
```

- [ ] **Step 3: Update `get` and `post` helpers**

Replace the existing `get` and `post` methods to use the correct client:

```go
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
```

Remove the entire old `const` block (`regBaseURL`, `defaultTimeout`, `smsWaitTimeout` — none are used after the refactor). The new constants block:

```go
const tempErrorText = "Попробуйте позже"
```

- [ ] **Step 4: Verify build (Register() body still has old signature — expected compile error)**

```bash
go build ./...
```

Expected: compile errors referencing `Register` signature mismatch. This is expected; the body will be replaced in Task 4.

- [ ] **Step 5: Commit the structural changes (with a stub Register)**

Replace the body of `Register()` with a temporary stub so it compiles:

```go
func (r *GHBRegistrar) Register(
	ctx context.Context,
	objectID string,
	regURL string,
	pd config.PersonalData,
	cfg config.RegistrationConfig,
	smsCodeFn SMSCodeFunc,
) error {
	return fmt.Errorf("not yet implemented")
}
```

```bash
go build ./...
```

Expected: compile errors in `main.go` because `reg.Register` call signature changed. Fix in `main.go` by adding `cfg.Registration` as argument (you may add a blank `cfg` temporarily — see Task 6 for proper wiring):

In `main.go`, find the `reg.Register(ctx, eid, regURL, cfg.PersonalData, smsCodeFn)` call and change it to:

```go
if err := reg.Register(ctx, eid, regURL, cfg.PersonalData, cfg.Registration, smsCodeFn); err != nil {
```

```bash
go build ./...
```

Expected: no output (success)

```bash
git add internal/registrar/ghb.go main.go
git commit -m "refactor(registrar): two-client struct, updated Registrar interface"
```

---

## Task 4: Implement full 5-step registration flow

**Files:**
- Modify: `internal/registrar/ghb.go`

- [ ] **Step 1: Replace `Register()` stub with outer 2-attempt loop and `doRegister()`**

Replace the stub `Register()` with:

```go
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
```

- [ ] **Step 2: Add `doRegister()` — Steps 1 and 2**

Add the first half of `doRegister` (Steps 1 and 2). The `time` package import must be present:

```go
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

	// ----------------------------------------------------------------
	// Step 1: GET — acquire session cookie
	// ----------------------------------------------------------------
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

	// ----------------------------------------------------------------
	// Step 2: POST — submit personal data; server sends SMS on success (302)
	// ----------------------------------------------------------------
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
```

- [ ] **Step 3: Add Steps 3, 3.5 and 4 to `doRegister()`**

Continue the function body (inside the same function, after the step2 loop):

```go
	// ----------------------------------------------------------------
	// Step 3: GET — verify SMS form is present
	// ----------------------------------------------------------------
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

	// ----------------------------------------------------------------
	// Step 3.5: Wait for SMS code
	// ----------------------------------------------------------------
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

	// ----------------------------------------------------------------
	// Step 4: POST — submit SMS confirmation code
	// ----------------------------------------------------------------
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
```

- [ ] **Step 4: Add Step 5 and close `doRegister()`**

Continue and close the function:

```go
	// ----------------------------------------------------------------
	// Step 5: GET — verify success page
	// ----------------------------------------------------------------
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
```

- [ ] **Step 5: Remove the old `Register()` body (the 4-step version)**

The old function body (everything between the old `func (r *GHBRegistrar) Register(...)` opening brace and closing brace) should now be fully replaced by the new `Register()` + `doRegister()`. Verify there is no dead code left.

Also remove the old `originOf` helper only if it's no longer referenced — it is still used in `post()`, so keep it.

- [ ] **Step 6: Verify build and tests**

```bash
go build ./...
go test ./internal/registrar/...
```

Expected: both pass with no errors.

- [ ] **Step 7: Commit**

```bash
git add internal/registrar/ghb.go
git commit -m "feat(registrar): 5-step flow, 301 re-POST, retry on temp errors, 2 attempts"
```

---

## Task 5: Update Telegram notifier — SMS request with deadline

**Files:**
- Modify: `internal/notifier/telegram.go`

- [ ] **Step 1: Add `FormatSMSCodeRequest` method**

Add this method to `telegram.go` after `FormatRegistrationOpened`:

```go
// FormatSMSCodeRequest formats the SMS code request message with a deadline.
func (t *Telegram) FormatSMSCodeRequest(deadline time.Time) string {
	return fmt.Sprintf(
		"📲 На ваш номер телефона отправлен код подтверждения.\n"+
			"Введите код до [%s] — иначе регистрация завершится с ошибкой.\n"+
			"Отправьте код ответным сообщением.",
		deadline.Format("02.01.2006 15:04:05"),
	)
}
```

The `time` package is already imported.

- [ ] **Step 2: Build**

```bash
go build ./...
```

Expected: no output.

- [ ] **Step 3: Commit**

```bash
git add internal/notifier/telegram.go
git commit -m "feat(notifier): add FormatSMSCodeRequest with deadline"
```

---

## Task 6: Update main.go — deadline in smsCodeFn, terminal success log

**Files:**
- Modify: `main.go`

- [ ] **Step 1: Update `smsCodeFn` to use deadline from context**

Find the `smsCodeFn` declaration block in `main.go` and replace it entirely:

```go
// smsCodeFn: ask the user for SMS code.
// If Telegram enabled: send message with deadline via Telegram and wait for reply.
// If Telegram disabled: prompt via terminal with deadline shown.
var smsCodeFn func(context.Context) (string, error)
if tg.IsEnabled() {
    smsCodeFn = func(innerCtx context.Context) (string, error) {
        deadline, _ := innerCtx.Deadline()
        msg := tg.FormatSMSCodeRequest(deadline)
        if err := tg.Send(innerCtx, msg); err != nil {
            log.Printf("telegram send error: %v", err)
            return "", err
        }
        log.Printf("[sms-code] waiting for SMS code from Telegram...")
        code, err := tg.WaitForCode(innerCtx, 0)
        if err != nil {
            return "", err
        }
        log.Printf("[sms-code] received code from Telegram: %s", code)
        return code, nil
    }
} else {
    smsCodeFn = func(innerCtx context.Context) (string, error) {
        deadline, _ := innerCtx.Deadline()
        log.Printf("[sms-code] введите SMS-код до [%s]:", deadline.Format("02.01.2006 15:04:05"))
        var code string
        fmt.Scanln(&code)
        log.Printf("[sms-code] received code from terminal: %s", code)
        return code, nil
    }
}
```

- [ ] **Step 2: Update event handler — add terminal success/error logs**

Find the goroutine inside the `handler` function that calls `reg.Register()`. Replace it entirely:

```go
if entry.AutoRegister {
    go func(eid string, data map[string]any) {
        regURL, _ := data["registration_url"].(string)
        if regURL == "" {
            log.Printf("missing registration_url for %s, skipping auto-register", eid)
            return
        }
        if err := reg.Register(ctx, eid, regURL, cfg.PersonalData, cfg.Registration, smsCodeFn); err != nil {
            log.Printf("auto-register error for %s: %v", eid, err)
            if tg.IsEnabled() {
                if sendErr := tg.Send(ctx, tg.FormatRegistrationError(eid, err)); sendErr != nil {
                    log.Printf("telegram send error: %v", sendErr)
                }
            } else {
                log.Printf("❌ Ошибка авторегистрации: %s — %v", eid, err)
            }
        } else {
            if tg.IsEnabled() {
                if sendErr := tg.Send(ctx, tg.FormatRegistrationSuccess(eid)); sendErr != nil {
                    log.Printf("telegram send error: %v", sendErr)
                }
            } else {
                log.Printf("✅ Авторегистрация выполнена: %s", eid)
            }
        }
    }(externalID, data)
}
```

- [ ] **Step 3: Build and verify**

```bash
go build ./...
go vet ./...
```

Expected: no output from either command.

- [ ] **Step 4: Commit**

```bash
git add main.go
git commit -m "feat(main): deadline in smsCodeFn, terminal success/error logs"
```

---

## Task 7: Update config.example.yaml and README

**Files:**
- Modify: `config.example.yaml`
- Modify: `README.md`

- [ ] **Step 1: Add `registration:` section to `config.example.yaml`**

Append after the `telegram:` block (before `personal_data:`):

```yaml
# Параметры авторегистрации (все поля опциональны — значения по умолчанию указаны ниже)
registration:
  # Максимальное время повтора при временных ошибках сервера ("Попробуйте позже"), мс
  retry_timeout_ms: 30000
  # Пауза между повторными попытками, мс
  retry_interval_ms: 500
  # Максимальное время ожидания SMS-кода от пользователя, секунды
  sms_code_wait_timeout_s: 180
  # HTTP-таймаут одного запроса регистрации, мс
  register_timeout_ms: 15000
```

- [ ] **Step 2: Update README — terminal mode section**

After the "## Режимы работы" section, replace the existing table and add new content:

```markdown
## Режимы работы

### Telegram-режим (`telegram.enabled: true`)

Весь диалог с пользователем идёт через Telegram:
- Уведомление об открытии регистрации — в чат.
- Запрос SMS-кода — сообщение с дедлайном в чат.
- Результат авторегистрации (успех / ошибка) — в чат.

| Настройка | Поведение |
|-----------|-----------|
| `notify_on_open: true`, `auto_register: false` | Уведомление в Telegram |
| `notify_on_open: true`, `auto_register: true`  | Уведомление + авторегистрация через Telegram |

### Терминальный режим (`telegram.enabled: false`)

Взаимодействие только через консоль:
- Открытие регистрации логируется в stdout.
- SMS-код вводится вручную в терминале при появлении запроса:
  ```
  [sms-code] введите SMS-код до [12.04.2026 15:30:00]:
  ```
- Результат авторегистрации выводится в stdout:
  ```
  ✅ Авторегистрация выполнена: obj-123
  ```
  или
  ```
  ❌ Ошибка авторегистрации: obj-123 — step 4: SMS code is incorrect
  ```

Для терминального режима укажите в конфиге `telegram.enabled: false` (или уберите секцию `telegram` полностью).
```

- [ ] **Step 3: Update README — add `registration:` config section description**

After the "Для авторегистрации..." sentence in the "Заполнить конфиг" section, add:

```markdown
Секция `registration` позволяет настроить таймауты — все поля опциональны, дефолтные значения приведены в `config.example.yaml`.
```

- [ ] **Step 4: Commit**

```bash
git add config.example.yaml README.md
git commit -m "docs: add registration config section and terminal mode docs"
```

---

## Task 8: Final build check and tag v1.0.0

- [ ] **Step 1: Run full test suite**

```bash
go test ./...
```

Expected:
```
ok  github.com/stroi-homes/worker-ghb-http/internal/config
ok  github.com/stroi-homes/worker-ghb-http/internal/registrar
```

- [ ] **Step 2: Run vet**

```bash
go vet ./...
```

Expected: no output.

- [ ] **Step 3: Build all platforms**

```bash
make dist
```

Expected: output like:
```
Building dist/worker-ghb-http-linux-amd64...
Building dist/worker-ghb-http-linux-arm64...
Building dist/worker-ghb-http-darwin-amd64...
Building dist/worker-ghb-http-darwin-arm64...
Building dist/worker-ghb-http-windows-amd64.exe...
```

- [ ] **Step 4: Tag v1.0.0**

```bash
git tag v1.0.0
```

(Do not push the tag — confirm with the user before `git push --tags`.)

- [ ] **Step 5: Verify version baked into binary**

```bash
./dist/worker-ghb-http-$(go env GOOS)-$(go env GOARCH) --version
```

Expected:
```
worker-ghb-http v1.0.0 (developer: ghb)
```
