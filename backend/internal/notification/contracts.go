// Package notification defines the channel-agnostic contracts for every outbound
// message in Malaeb — OTP delivery and booking-lifecycle events.
//
// PART 1 scope: INTERFACES AND TYPES ONLY. There is deliberately no provider
// code, no routing logic, and no OTP/business logic in this package. Concrete
// channel adapters (WhatsApp, SMS, email, Fake) and the NotificationService that
// routes across them are built in later PARTs and MUST satisfy these contracts
// without modifying them. Per the architecture rules, no WhatsApp/Meta SDK call
// may appear anywhere except inside its dedicated adapter file.
package notification

import (
	"context"
	"time"
)

// MessageKind is the channel-agnostic semantic of an outbound message. Adapters
// map each kind onto their provider-specific representation — e.g. a WhatsApp
// UTILITY template for booking events, or an AUTHENTICATION template / SMS for
// OTP. The set of kinds is fixed here; channels render them, they do not invent
// new ones.
type MessageKind string

const (
	KindOTP              MessageKind = "otp"
	KindBookingConfirmed MessageKind = "booking_confirmed"
	KindBookingRejected  MessageKind = "booking_rejected"
	KindBookingCancelled MessageKind = "booking_cancelled"
)

// DeliveryStatus is the outcome a channel reports for a single send attempt.
type DeliveryStatus string

const (
	DeliveryQueued    DeliveryStatus = "queued"
	DeliverySent      DeliveryStatus = "sent"
	DeliveryDelivered DeliveryStatus = "delivered"
	DeliveryFailed    DeliveryStatus = "failed"
)

// Payload is the typed body of an OutboundMessage. Each MessageKind has exactly
// one corresponding payload type, and every payload reports the kind it belongs
// to — so routing and rendering never depend on scattered type assertions.
type Payload interface {
	Kind() MessageKind
}

// OTPPayload carries a one-time code. Per Meta's rules the OTP message BODY is
// FIXED by the AUTHENTICATION template; only the code is variable (the button
// type is chosen by the adapter). Do NOT add free-form body text here.
type OTPPayload struct {
	Code             string
	ExpiresInSeconds int
}

// Kind reports the message kind this payload belongs to.
func (OTPPayload) Kind() MessageKind { return KindOTP }

// BookingConfirmedPayload describes a confirmed booking for the player.
type BookingConfirmedPayload struct {
	BookingID int64
	PitchName string
	StartTime time.Time
	EndTime   time.Time
}

// Kind reports the message kind this payload belongs to.
func (BookingConfirmedPayload) Kind() MessageKind { return KindBookingConfirmed }

// BookingRejectedPayload describes a rejected (previously pending) booking.
type BookingRejectedPayload struct {
	BookingID int64
	PitchName string
	StartTime time.Time
	EndTime   time.Time
	Reason    string
}

// Kind reports the message kind this payload belongs to.
func (BookingRejectedPayload) Kind() MessageKind { return KindBookingRejected }

// BookingCancelledPayload describes a cancelled (previously confirmed) booking.
type BookingCancelledPayload struct {
	BookingID int64
	PitchName string
	StartTime time.Time
	EndTime   time.Time
	Reason    string
}

// Kind reports the message kind this payload belongs to.
func (BookingCancelledPayload) Kind() MessageKind { return KindBookingCancelled }

// OutboundMessage is a single channel-agnostic message handed to a channel for
// delivery. Recipient is always an E.164 phone number (e.g. +9627XXXXXXXX);
// Payload must be the concrete type matching Kind.
type OutboundMessage struct {
	Recipient string
	Kind      MessageKind
	Payload   Payload
}

// DeliveryResult is what a channel returns for an attempted send.
// ProviderMessageID is the upstream provider's identifier when available; Err
// holds the failure when Status is DeliveryFailed.
type DeliveryResult struct {
	Status            DeliveryStatus
	ProviderMessageID string
	Err               error
}

// NotificationChannel is the single interface every delivery adapter (WhatsApp,
// SMS, email, Fake) implements. Channels are interchangeable: the
// NotificationService selects one and calls Send.
type NotificationChannel interface {
	Send(ctx context.Context, msg OutboundMessage) (DeliveryResult, error)
}

// OtpService is the contract for requesting and verifying one-time passcodes.
// Implementations decide storage, expiry, rate limiting, and which channel to
// dispatch through — none of which lives in this package.
type OtpService interface {
	Request(ctx context.Context, phone string) error
	Verify(ctx context.Context, phone, code string) (bool, error)
}
