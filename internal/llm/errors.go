package llm

import (
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
	if err == nil {
		return false
	}
	var httpErr *HTTPStatusError
	// Unwrap manually since errors.As works transitively
	if unwrapped := unwrapHTTPStatusError(err); unwrapped != nil {
		httpErr = unwrapped
	}
	if httpErr == nil {
		return false
	}
	code := httpErr.StatusCode
	return code == 429 || (code >= 500 && code < 600)
}

func unwrapHTTPStatusError(err error) *HTTPStatusError {
	for err != nil {
		if httpErr, ok := err.(*HTTPStatusError); ok {
			return httpErr
		}
		type unwrapper interface{ Unwrap() error }
		if u, ok := err.(unwrapper); ok {
			err = u.Unwrap()
		} else {
			break
		}
	}
	return nil
}
