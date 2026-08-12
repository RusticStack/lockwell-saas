package billing

import "errors"

var ErrConflictingReplay = errors.New("Stripe event ID replayed with different payload")
