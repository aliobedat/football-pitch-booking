package otp

// PART 3A scope: OTP service core. This file carries the request IP through
// context.Context so OtpService.Request can rate-limit per IP WITHOUT changing
// the OtpService interface signature (which takes only ctx + phone). The HTTP
// layer (PART 3B) injects the caller IP via WithIP; the service reads it via
// ipFromContext.

import "context"

// ipContextKey is an unexported type so the context value cannot collide with
// keys set by other packages.
type ipContextKey struct{}

// WithIP returns a child context carrying the caller's IP address. The HTTP
// layer is expected to call this (e.g. from gin's c.ClientIP()) before invoking
// OtpService.Request. An empty ip is stored as-is and treated as "unknown" by
// the service, which then skips per-IP rate limiting for that request.
func WithIP(ctx context.Context, ip string) context.Context {
	return context.WithValue(ctx, ipContextKey{}, ip)
}

// ipFromContext extracts the caller IP previously stored by WithIP. The boolean
// reports whether a non-empty IP was present.
func ipFromContext(ctx context.Context) (string, bool) {
	ip, _ := ctx.Value(ipContextKey{}).(string)
	return ip, ip != ""
}
