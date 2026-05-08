package tracker

import "errors"

var (
	// ErrLinearReplyCommentsUnsupported signals that the Linear schema or
	// configured client cannot create threaded replies.
	ErrLinearReplyCommentsUnsupported = errors.New("linear reply comments unsupported")

	// ErrLinearCommentUpdateUnsupported signals that an existing comment cannot
	// be updated and callers should append a superseding comment instead.
	ErrLinearCommentUpdateUnsupported = errors.New("linear comment update unsupported")
)
