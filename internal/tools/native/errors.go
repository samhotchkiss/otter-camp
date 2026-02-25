package native

import "errors"

var (
	ErrUnknownTool   = errors.New("native: unknown tool")
	ErrPathTraversal = errors.New("native: path traversal")
)
