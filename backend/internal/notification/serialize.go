package notification

// Serialization for the Postgres-backed outbox (PART 6). An OutboundMessage
// carries a Payload interface, so it cannot be round-tripped through JSON
// generically — the concrete payload type must be chosen on the way back in.
// That knowledge (the MessageKind → payload-type mapping) lives here, alongside
// the payload definitions, rather than leaking into the outbox package. The
// outbox stores and loads opaque bytes; only this file understands their shape.

import (
	"encoding/json"
	"fmt"
)

// outboundEnvelope is the on-the-wire shape persisted for a queued message. The
// payload is kept as raw JSON until the kind tells us how to decode it.
type outboundEnvelope struct {
	Recipient string          `json:"recipient"`
	Kind      MessageKind     `json:"kind"`
	Payload   json.RawMessage `json:"payload"`
}

// MarshalOutbound encodes an OutboundMessage for durable storage. It validates
// the message first, so a malformed message is rejected at enqueue time rather
// than surfacing as an undecodable job later.
func MarshalOutbound(msg OutboundMessage) ([]byte, error) {
	if err := validate(msg); err != nil {
		return nil, err
	}
	payloadJSON, err := json.Marshal(msg.Payload)
	if err != nil {
		return nil, fmt.Errorf("notification: marshal payload: %w", err)
	}
	return json.Marshal(outboundEnvelope{
		Recipient: msg.Recipient,
		Kind:      msg.Kind,
		Payload:   payloadJSON,
	})
}

// UnmarshalOutbound reconstructs an OutboundMessage previously produced by
// MarshalOutbound, selecting the concrete payload type from the stored kind.
// An unknown kind or a payload that does not match its kind is an error, so a
// corrupt job fails loudly instead of dispatching a half-formed message.
func UnmarshalOutbound(data []byte) (OutboundMessage, error) {
	var env outboundEnvelope
	if err := json.Unmarshal(data, &env); err != nil {
		return OutboundMessage{}, fmt.Errorf("notification: unmarshal envelope: %w", err)
	}

	var payload Payload
	switch env.Kind {
	case KindOTP:
		var p OTPPayload
		if err := json.Unmarshal(env.Payload, &p); err != nil {
			return OutboundMessage{}, decodeErr(env.Kind, err)
		}
		payload = p
	case KindBookingConfirmed:
		var p BookingConfirmedPayload
		if err := json.Unmarshal(env.Payload, &p); err != nil {
			return OutboundMessage{}, decodeErr(env.Kind, err)
		}
		payload = p
	case KindBookingRejected:
		var p BookingRejectedPayload
		if err := json.Unmarshal(env.Payload, &p); err != nil {
			return OutboundMessage{}, decodeErr(env.Kind, err)
		}
		payload = p
	case KindBookingCancelled:
		var p BookingCancelledPayload
		if err := json.Unmarshal(env.Payload, &p); err != nil {
			return OutboundMessage{}, decodeErr(env.Kind, err)
		}
		payload = p
	case KindBookingReminder:
		var p BookingReminderPayload
		if err := json.Unmarshal(env.Payload, &p); err != nil {
			return OutboundMessage{}, decodeErr(env.Kind, err)
		}
		payload = p
	default:
		return OutboundMessage{}, fmt.Errorf("%w: unknown kind %q", ErrInvalidMessage, env.Kind)
	}

	msg := OutboundMessage{Recipient: env.Recipient, Kind: env.Kind, Payload: payload}
	if err := validate(msg); err != nil {
		return OutboundMessage{}, err
	}
	return msg, nil
}

func decodeErr(kind MessageKind, err error) error {
	return fmt.Errorf("%w: decode %s payload: %v", ErrInvalidMessage, kind, err)
}
