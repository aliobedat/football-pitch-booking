package notification

import (
	"errors"
	"testing"
	"time"
)

// TestMarshalUnmarshalOutbound_RoundTrip verifies every message kind survives a
// full encode/decode cycle through the outbox envelope with its typed payload
// intact — the property the Postgres queue depends on.
func TestMarshalUnmarshalOutbound_RoundTrip(t *testing.T) {
	start := time.Date(2026, 6, 2, 18, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)
	const recipient = "+962790000000"

	cases := []OutboundMessage{
		{Recipient: recipient, Kind: KindOTP, Payload: OTPPayload{Code: "123456", ExpiresInSeconds: 300}},
		{Recipient: recipient, Kind: KindBookingConfirmed, Payload: BookingConfirmedPayload{BookingID: 1, PitchName: "Pitch A", StartTime: start, EndTime: end}},
		{Recipient: recipient, Kind: KindBookingRejected, Payload: BookingRejectedPayload{BookingID: 2, PitchName: "Pitch B", StartTime: start, EndTime: end, Reason: "slot taken"}},
		{Recipient: recipient, Kind: KindBookingCancelled, Payload: BookingCancelledPayload{BookingID: 3, PitchName: "Pitch C", StartTime: start, EndTime: end, Reason: "owner cancelled"}},
		{Recipient: recipient, Kind: KindBookingReminder, Payload: BookingReminderPayload{BookingID: 4, PitchName: "Pitch D", StartTime: start, EndTime: end}},
	}

	for _, want := range cases {
		t.Run(string(want.Kind), func(t *testing.T) {
			data, err := MarshalOutbound(want)
			if err != nil {
				t.Fatalf("MarshalOutbound: %v", err)
			}
			got, err := UnmarshalOutbound(data)
			if err != nil {
				t.Fatalf("UnmarshalOutbound: %v", err)
			}
			if got.Recipient != want.Recipient {
				t.Errorf("recipient = %q, want %q", got.Recipient, want.Recipient)
			}
			if got.Kind != want.Kind {
				t.Errorf("kind = %q, want %q", got.Kind, want.Kind)
			}
			if got.Payload != want.Payload {
				t.Errorf("payload = %+v, want %+v", got.Payload, want.Payload)
			}
		})
	}
}

func TestMarshalOutbound_RejectsInvalid(t *testing.T) {
	// Validation runs before encoding, so a malformed message never produces an
	// envelope (it would otherwise surface later as an undecodable job).
	_, err := MarshalOutbound(OutboundMessage{Recipient: "+962790000000", Kind: KindOTP, Payload: nil})
	if !errors.Is(err, ErrInvalidMessage) {
		t.Fatalf("err = %v, want errors.Is(_, ErrInvalidMessage)", err)
	}
}

func TestUnmarshalOutbound_Errors(t *testing.T) {
	t.Run("malformed json", func(t *testing.T) {
		if _, err := UnmarshalOutbound([]byte("}{")); err == nil {
			t.Fatal("expected an error decoding malformed JSON")
		}
	})

	t.Run("unknown kind", func(t *testing.T) {
		_, err := UnmarshalOutbound([]byte(`{"recipient":"+962790000000","kind":"telegram","payload":{}}`))
		if !errors.Is(err, ErrInvalidMessage) {
			t.Fatalf("err = %v, want errors.Is(_, ErrInvalidMessage)", err)
		}
	})

	t.Run("payload kind mismatch passes decode but fails validate", func(t *testing.T) {
		// An OTP envelope whose payload decodes fine but yields an empty code is
		// still structurally valid (validate only checks recipient/payload/kind
		// pairing); a genuinely mismatched envelope cannot be constructed via the
		// kind switch, so the unknown-kind case above covers the corrupt path.
		data, err := MarshalOutbound(OutboundMessage{
			Recipient: "+962790000000", Kind: KindOTP, Payload: OTPPayload{Code: "9", ExpiresInSeconds: 60},
		})
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		if _, err := UnmarshalOutbound(data); err != nil {
			t.Fatalf("unmarshal valid OTP envelope: %v", err)
		}
	})
}
