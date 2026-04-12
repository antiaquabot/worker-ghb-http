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
