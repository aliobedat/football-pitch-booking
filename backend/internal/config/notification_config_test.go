package config

import "testing"

func TestLoadTwilioConfig_NoneIsUnconfigured(t *testing.T) {
	t.Setenv("TWILIO_ACCOUNT_SID", "")
	t.Setenv("TWILIO_AUTH_TOKEN", "")
	t.Setenv("TWILIO_FROM_NUMBER", "")
	c := loadTwilioConfig()
	if c.Configured() {
		t.Error("empty Twilio config reported Configured()=true")
	}
}

func TestLoadTwilioConfig_AllIsConfigured(t *testing.T) {
	t.Setenv("TWILIO_ACCOUNT_SID", "ACxxx")
	t.Setenv("TWILIO_AUTH_TOKEN", "tok")
	t.Setenv("TWILIO_FROM_NUMBER", "+15005550006")
	c := loadTwilioConfig()
	if !c.Configured() {
		t.Error("full Twilio config reported Configured()=false")
	}
}

// A PARTIAL Twilio configuration must fail fast (panic) — silently disabling SMS
// because one var was forgotten is the dangerous case.
func TestLoadTwilioConfig_PartialPanics(t *testing.T) {
	t.Setenv("TWILIO_ACCOUNT_SID", "ACxxx")
	t.Setenv("TWILIO_AUTH_TOKEN", "")
	t.Setenv("TWILIO_FROM_NUMBER", "+15005550006")
	defer func() {
		if recover() == nil {
			t.Error("partial Twilio config did not panic")
		}
	}()
	_ = loadTwilioConfig()
}

func TestLoadNotificationConfig_BetaDefaults(t *testing.T) {
	t.Setenv("NOTIFY_OTP_ROUTE", "")
	t.Setenv("NOTIFY_BOOKING_ROUTE", "")
	t.Setenv("NOTIFY_DEFAULT_ROUTE", "")
	c := loadNotificationConfig()
	if c.OTPRoute != "twilio_sms" {
		t.Errorf("OTPRoute = %q, want twilio_sms", c.OTPRoute)
	}
	if c.BookingRoute != "log_only" || c.DefaultRoute != "log_only" {
		t.Errorf("booking/default routes = %q/%q, want log_only/log_only", c.BookingRoute, c.DefaultRoute)
	}
}
