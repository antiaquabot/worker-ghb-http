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
	if r.SMSCodeWaitTimeoutS != 60 {
		t.Errorf("SMSCodeWaitTimeoutS should not be overwritten, got %d", r.SMSCodeWaitTimeoutS)
	}
	if r.RegisterTimeoutMs != 8000 {
		t.Errorf("RegisterTimeoutMs should not be overwritten, got %d", r.RegisterTimeoutMs)
	}
}
