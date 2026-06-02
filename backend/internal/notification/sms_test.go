package notification

import (
	"context"
	"strings"
	"testing"
)

// TestSmsChannel_AlwaysSucceeds confirms the fallback channel of last resort
// accepts every kind, records it, and returns a successful result with a
// prefixed provider id — never an error.
func TestSmsChannel_AlwaysSucceeds(t *testing.T) {
	kinds := []MessageKind{KindOTP, KindBookingConfirmed, KindBookingRejected, KindBookingCancelled}
	sms := NewSmsChannel(SmsSilent())

	for _, kind := range kinds {
		res, err := sms.Send(context.Background(), sampleMessage(kind))
		if err != nil {
			t.Fatalf("Send(%s) error: %v", kind, err)
		}
		if res.Status != DeliverySent {
			t.Errorf("Status = %q, want %q", res.Status, DeliverySent)
		}
		if !strings.HasPrefix(res.ProviderMessageID, "sms_") {
			t.Errorf("ProviderMessageID = %q, want sms_ prefix", res.ProviderMessageID)
		}
	}

	if sms.Count() != len(kinds) {
		t.Errorf("Count = %d, want %d", sms.Count(), len(kinds))
	}
	if last, ok := sms.Last(); !ok || last.Kind != KindBookingCancelled {
		t.Errorf("Last = (%v, %v), want last cancelled message", last.Kind, ok)
	}

	sms.Reset()
	if sms.Count() != 0 {
		t.Errorf("Count after Reset = %d, want 0", sms.Count())
	}
}

var _ NotificationChannel = (*SmsChannel)(nil)
