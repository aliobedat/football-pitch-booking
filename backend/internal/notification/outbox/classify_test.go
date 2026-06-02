package outbox

import (
	"errors"
	"fmt"
	"testing"

	"github.com/ali/football-pitch-api/internal/notification"
)

func TestClassify(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want disposition
	}{
		{"nil defends to retry", nil, dispositionRetry},
		{"generic error retries", errors.New("connection reset"), dispositionRetry},
		{"opted out blocks", notification.ErrOptedOut, dispositionBlocked},
		{"opt-in required blocks", notification.ErrOptInRequired, dispositionBlocked},
		{"invalid message blocks", notification.ErrInvalidMessage, dispositionBlocked},
		{"no opt-in checker blocks", notification.ErrNoOptInChecker, dispositionBlocked},
		{"unknown channel dead-letters", notification.ErrUnknownChannel, dispositionDeadLetter},
		{"invalid channel dead-letters", notification.ErrInvalidChannel, dispositionDeadLetter},
		// errors.Is must see through wrapping.
		{"wrapped opt-out still blocks", fmt.Errorf("dispatch: %w", notification.ErrOptedOut), dispositionBlocked},
		{"wrapped unknown channel still dead-letters", fmt.Errorf("route: %w", notification.ErrUnknownChannel), dispositionDeadLetter},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := classify(c.err); got != c.want {
				t.Errorf("classify(%v) = %d, want %d", c.err, got, c.want)
			}
		})
	}
}
