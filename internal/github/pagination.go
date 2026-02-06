package github

import (
	"context"
	"errors"
	"net/http"
	"strings"
)

// FetchNextPage fetches one page and updates the provided checkpoint.
// It returns a PauseError when processing should pause and resume later.
func (c *Client) FetchNextPage(
	ctx context.Context,
	jobType JobType,
	checkpoint PaginationCheckpoint,
	startEndpoint string,
) (*Response, PaginationCheckpoint, error) {
	updatedCheckpoint := checkpoint
	endpoint := strings.TrimSpace(updatedCheckpoint.NextURL)
	if endpoint == "" {
		endpoint = strings.TrimSpace(startEndpoint)
	}
	if endpoint == "" {
		return nil, updatedCheckpoint, errors.New("start endpoint is required")
	}

	if updatedCheckpoint.PausedUntil != nil && c.now().Before(*updatedCheckpoint.PausedUntil) {
		pauseError := &PauseError{ResumeAt: *updatedCheckpoint.PausedUntil, Reason: updatedCheckpoint.PauseReason}
		return nil, updatedCheckpoint, pauseError
	}

	request, err := c.NewRequest(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, updatedCheckpoint, err
	}

	response, err := c.Do(ctx, jobType, request)
	if err != nil {
		var rateLimitError *RateLimitError
		if errors.As(err, &rateLimitError) {
			resumeAt := c.now().Add(rateLimitError.RetryAfter)
			updatedCheckpoint.PausedUntil = &resumeAt
			updatedCheckpoint.PauseReason = "rate_limited"
			updatedCheckpoint.LastRateLimit = rateLimitError.RateLimit
			return nil, updatedCheckpoint, &PauseError{ResumeAt: resumeAt, Reason: "rate limited"}
		}

		var pauseError *PauseError
		if errors.As(err, &pauseError) {
			updatedCheckpoint.PausedUntil = &pauseError.ResumeAt
			updatedCheckpoint.PauseReason = pauseError.Reason
			updatedCheckpoint.LastRateLimit = c.CurrentRateLimit()
			return nil, updatedCheckpoint, pauseError
		}

		return nil, updatedCheckpoint, err
	}

	updatedCheckpoint.NextURL = response.NextPage
	updatedCheckpoint.LastRateLimit = response.RateLimit
	updatedCheckpoint.PausedUntil = nil
	updatedCheckpoint.PauseReason = ""
	return response, updatedCheckpoint, nil
}
