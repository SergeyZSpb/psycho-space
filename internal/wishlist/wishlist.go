// Package wishlist is the first app section: users add ideas and upvote them.
// It is designed to be long-lived (one of several planned sections).
package wishlist

import (
	"errors"
	"time"
)

// Sort controls list ordering.
type Sort string

const (
	SortTop Sort = "top"
	SortNew Sort = "new"

	maxTitle       = 200
	maxBody        = 4000
	maxCommentBody = 2000
)

// Item is a wishlist idea with its vote tally (from the viewer's perspective).
type Item struct {
	ID           string
	AuthorID     string
	Title        string
	Body         string
	Votes        int
	VotedByMe    bool
	CommentCount int
	CreatedAt    time.Time
}

// Comment is a comment on an item, itself upvotable.
type Comment struct {
	ID        string
	ItemID    string
	AuthorID  string
	Body      string
	Votes     int
	VotedByMe bool
	CreatedAt time.Time
}

// Errors.
var (
	ErrNotFound        = errors.New("wishlist: item not found")
	ErrCommentNotFound = errors.New("wishlist: comment not found")
	ErrEmptyTitle      = errors.New("wishlist: title required")
	ErrEmptyComment    = errors.New("wishlist: comment body required")
	ErrTooLong         = errors.New("wishlist: field too long")
	ErrForbidden       = errors.New("wishlist: not allowed")
)
