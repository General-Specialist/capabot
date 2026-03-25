package llm

import (
	"errors"
	"fmt"
	"io"
	"net/http"
)

// HTTPStatusError represents an HTTP error response.
type HTTPStatusError struct {
	StatusCode int
	Body       string
}

func (e *HTTPStatusError) Error() string {
	return fmt.Sprintf("HTTP %d: %s", e.StatusCode, e.Body)
}

// httpStatusError reads the response body and returns an HTTPStatusError.
func httpStatusError(resp *http.Response) *HTTPStatusError {
	body, _ := io.ReadAll(resp.Body)
	return &HTTPStatusError{
		StatusCode: resp.StatusCode,
		Body:       string(body),
	}
}

// isRetryable reports whether an error warrants a retry (429 or 5xx).
func isRetryable(err error) bool {
	var httpErr *HTTPStatusError
	if !errors.As(err, &httpErr) {
		return false
	}
	return httpErr.StatusCode == 429 || (httpErr.StatusCode >= 500 && httpErr.StatusCode < 600)
}
