package account

import "errors"

// ErrNotFound is returned when an account does not exist (or is soft-deleted).
var ErrNotFound = errors.New("account: not found")
