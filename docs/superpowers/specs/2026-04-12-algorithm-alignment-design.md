# Design: Algorithm Alignment + Dual Mode + Release Prep

**Date:** 2026-04-12
**Status:** Approved

## Context

The Go worker (`worker-ghb-http`) is a standalone rewrite of the Python
`auto_registration_api_worker.py`. An audit revealed several algorithm
divergences that cause silent failures in production. This spec describes
the changes needed to align the registration flow, complete the dual-mode
(Telegram / terminal) support, and ship v1.0.0.

---

## 1. Registration Algorithm (`internal/registrar/ghb.go`)

### 1.1 Full 5-step flow

| Step | Method | Purpose | Success condition |
|------|--------|---------|-------------------|
| 1 | GET  | Acquire PHPSESSID cookie | HTTP 200 |
| 2 | POST `act=reg_user` | Submit personal data; server sends SMS | HTTP 302 |
| 3 | GET  | Verify SMS form is present | `sms_code` in body |
| 3.5 | — | Wait for SMS code via `smsCodeFn` | non-empty string |
| 4 | POST `act=conf_user` | Submit SMS code | HTTP 302 |
| 5 | GET  | Verify success page | "Регистрация завершена" in body |

Currently the Go worker is missing Step 5 and has a broken Step 2.

### 1.2 301 redirect handling in Step 2

**Problem:** Go's `http.Client` follows 301 by converting POST → GET, so the
form data is never sent to the canonical HTTPS URL.

**Fix:** `GHBRegistrar` uses **two** `http.Client` instances that share the
same `cookiejar.Jar`:

- `getClient` — standard redirect-following client (used for Step 1, 3, 5 GET requests).
- `postClient` — client whose `CheckRedirect` returns `http.ErrUseLastResponse`
  on every redirect, so the caller receives the raw 301/302 response.

On 301 the caller reads `Location`, updates `postURL`, `Origin`, and `Referer`,
and re-issues the POST. The loop runs until 302 (success) is received or
`retryTimeout` expires.

### 1.3 Retry on temporary errors

Steps 2 and 4 retry when the HTML body contains "Попробуйте позже". Retry
loop sleeps `retryInterval` between attempts and stops after `retryTimeout`.

### 1.4 HTML error extraction

```go
func extractError(body string) string
```
Uses `regexp` to find `class="[^"]*megaalert-content[^"]*"` and returns the
inner text with HTML tags stripped and whitespace normalised. Called after
every HTTP response.

### 1.5 Two registration attempts

`Register()` wraps the 5-step flow in a `for attempt := 0; attempt < 2; attempt++`
loop. The second attempt only runs if the first returns a non-nil error.
A 500 ms pause separates attempts.

### 1.6 Updated signature

```go
func (r *GHBRegistrar) Register(
    ctx context.Context,
    objectID string,
    regURL string,
    pd config.PersonalData,
    cfg config.RegistrationConfig,
    smsCodeFn SMSCodeFunc,
) error
```

`RegistrationConfig` carries the four configurable timeouts.

---

## 2. Configuration (`internal/config/config.go`, `config.example.yaml`)

New struct added to `Config`:

```go
type RegistrationConfig struct {
    RetryTimeoutMs      int `yaml:"retry_timeout_ms"`       // default 30000
    RetryIntervalMs     int `yaml:"retry_interval_ms"`      // default 500
    SMSCodeWaitTimeoutS int `yaml:"sms_code_wait_timeout_s"` // default 180
    RegisterTimeoutMs   int `yaml:"register_timeout_ms"`    // default 15000
}
```

All fields are optional; zero values are replaced with defaults in `Load()`.
`config.example.yaml` gets a `registration:` section with commented defaults.

---

## 3. Notifications

### 3.1 Telegram — SMS request with deadline

New method on `*Telegram`:

```go
func (t *Telegram) FormatSMSCodeRequest(deadline time.Time) string
```

Format (mirrors Python):
```
📲 На ваш номер телефона отправлен код подтверждения.
Введите код до [12.04.2026 15:30:00] — иначе регистрация завершится с ошибкой.
Отправьте код ответным сообщением.
```

### 3.2 Terminal mode — SMS prompt with deadline

`smsCodeFn` in terminal mode prints:
```
[sms-code] введите SMS-код до [12.04.2026 15:30:00]:
```
then reads a line from stdin.

### 3.3 Terminal mode — success notification

After `reg.Register()` returns nil in terminal mode:
```go
log.Printf("✅ Авторегистрация выполнена: %s", eid)
```

---

## 4. Release Preparation

### 4.1 README.md additions
- Section "Терминальный режим" explaining `telegram.enabled: false` workflow
  and how SMS code is entered via stdin.
- Updated "Режимы работы" table to include terminal-mode rows.
- Document the new `registration:` config section.

### 4.2 config.example.yaml
- Add `registration:` section with all four fields, commented defaults.

### 4.3 Build / tagging
- `go build ./...` and `go vet ./...` must pass before tagging.
- Tag `v1.0.0` after all changes are committed.

---

## Out of Scope

- HTTP exchange logging to admin chat (Python-only, requires admin chat concept).
- Mini App inline keyboard in SMS request (no Mini App in this worker).
- Redis stream / DB task queue (architectural difference; Go worker is self-contained).
