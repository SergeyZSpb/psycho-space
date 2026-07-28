package account

import "errors"

// ErrNotFound is returned when an account does not exist (or is soft-deleted).
var ErrNotFound = errors.New("account: not found")

// ErrAlreadyForgotten is returned when an account has already been anonymised.
// Reported rather than silently succeeding, because "forget this person" is an
// irreversible action and an admin pressing it twice deserves to know the first
// one worked rather than to wonder.
var ErrAlreadyForgotten = errors.New("account: already forgotten")
