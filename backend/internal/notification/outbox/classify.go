package outbox

import (
	"errors"

	"github.com/ali/football-pitch-api/internal/notification"
)

// disposition is the worker's verdict on a failed send: should it retry, give
// up permanently as a delivery failure, or refuse permanently on policy grounds.
type disposition int

const (
	// dispositionRetry — a transient failure (network, provider 5xx, timeout).
	// Reschedule with backoff until the attempt budget is spent.
	dispositionRetry disposition = iota
	// dispositionDeadLetter — a failure that will never succeed on retry (e.g.
	// an unrecognised channel). Dead-letter immediately rather than burn retries.
	dispositionDeadLetter
	// dispositionBlocked — a policy/validation refusal (recipient opted out,
	// malformed message). Terminal and NOT a delivery failure.
	dispositionBlocked
)

// classify maps a send failure onto a disposition. The default — for any error
// the queue does not specifically recognise — is to retry, because most send
// failures (provider outages, transient network faults) are recoverable. Only
// the explicitly permanent cases short-circuit.
func classify(err error) disposition {
	switch {
	case err == nil:
		return dispositionRetry // defensive: callers only classify failures
	case errors.Is(err, notification.ErrOptedOut),
		errors.Is(err, notification.ErrOptInRequired):
		// Consent refusals: the recipient's state must change (re-opt-in /
		// reverse opt-out) before any send can succeed. Never retry.
		return dispositionBlocked
	case errors.Is(err, notification.ErrInvalidMessage),
		errors.Is(err, notification.ErrNoOptInChecker):
		// A structurally broken job or misconfigured gate cannot self-heal.
		return dispositionBlocked
	case errors.Is(err, notification.ErrUnknownChannel),
		errors.Is(err, notification.ErrInvalidChannel):
		// Active channel is not registered/recognised — a deployment fault, not
		// a per-recipient transient. Dead-letter so it surfaces in alerting.
		return dispositionDeadLetter
	default:
		return dispositionRetry
	}
}
