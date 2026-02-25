package turn

import "errors"

var (
	ErrAuthFailed  = errors.New("model provider authentication failed")
	ErrRateLimited = errors.New("model provider rate limited")
)
