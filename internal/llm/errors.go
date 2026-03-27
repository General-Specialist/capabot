package llm

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"google.golang.org/genai"
)

// ErrorKind classifies provider errors into actionable categories.
type ErrorKind int

const (
	ErrUnknown        ErrorKind = iota
	ErrAuth                     // 401/403 — bad or missing API key
	ErrRateLimit                // 429 — too many requests
	ErrOverloaded               // 529 / 503 — provider temporarily overloaded
	ErrContextLength            // request exceeds model context window
	ErrContentFiltered          // safety filter / content policy block
	ErrInvalidRequest           // 400 — malformed request, bad params
	ErrModelNotFound            // requested model doesn't exist
	ErrInsufficientQuota        // billing / quota exhausted
	ErrServerError              // 500+ — provider-side failure
)

func (k ErrorKind) String() string {
	switch k {
	case ErrAuth:
		return "authentication_error"
	case ErrRateLimit:
		return "rate_limit"
	case ErrOverloaded:
		return "overloaded"
	case ErrContextLength:
		return "context_length_exceeded"
	case ErrContentFiltered:
		return "content_filtered"
	case ErrInvalidRequest:
		return "invalid_request"
	case ErrModelNotFound:
		return "model_not_found"
	case ErrInsufficientQuota:
		return "insufficient_quota"
	case ErrServerError:
		return "server_error"
	default:
		return "unknown"
	}
}

// ProviderError is a classified error from an LLM provider.
type ProviderError struct {
	Kind       ErrorKind
	Provider   string
	StatusCode int
	Message    string // user-friendly message
	Cause      error  // original error
}

func (e *ProviderError) Error() string {
	if e.Message != "" {
		return fmt.Sprintf("%s (%s): %s", e.Provider, e.Kind, e.Message)
	}
	return fmt.Sprintf("%s (%s): HTTP %d", e.Provider, e.Kind, e.StatusCode)
}

func (e *ProviderError) Unwrap() error { return e.Cause }

// IsRetryable reports whether this error warrants a retry with the same or another provider.
func (e *ProviderError) IsRetryable() bool {
	switch e.Kind {
	case ErrRateLimit, ErrOverloaded, ErrServerError:
		return true
	default:
		return false
	}
}

// HTTPStatusError represents a raw HTTP error response (kept for backward compat with isRetryable).
type HTTPStatusError struct {
	StatusCode int
	Body       string
}

func (e *HTTPStatusError) Error() string {
	return fmt.Sprintf("HTTP %d: %s", e.StatusCode, e.Body)
}

// isRetryable reports whether an error warrants a retry (429 or 5xx).
func isRetryable(err error) bool {
	var pe *ProviderError
	if errors.As(err, &pe) {
		return pe.IsRetryable()
	}
	var httpErr *HTTPStatusError
	if errors.As(err, &httpErr) {
		return httpErr.StatusCode == 429 || (httpErr.StatusCode >= 500 && httpErr.StatusCode < 600)
	}
	return false
}

// --- Provider-specific error classification ---

// classifyOpenAIError parses an OpenAI/OpenRouter error response and returns a ProviderError.
func classifyOpenAIError(provider string, resp *http.Response) *ProviderError {
	body, _ := io.ReadAll(resp.Body)
	bodyStr := string(body)

	pe := &ProviderError{
		Provider:   provider,
		StatusCode: resp.StatusCode,
		Cause:      &HTTPStatusError{StatusCode: resp.StatusCode, Body: bodyStr},
	}

	// Parse OpenAI error envelope: {"error": {"message": "...", "type": "...", "code": "..."}}
	var envelope struct {
		Error struct {
			Message string `json:"message"`
			Type    string `json:"type"`
			Code    any    `json:"code"` // string or null
		} `json:"error"`
	}
	if json.Unmarshal(body, &envelope) == nil && envelope.Error.Message != "" {
		pe.Message = envelope.Error.Message
		code, _ := envelope.Error.Code.(string)

		switch {
		case resp.StatusCode == 401 || resp.StatusCode == 403:
			pe.Kind = ErrAuth
		case resp.StatusCode == 429:
			if code == "insufficient_quota" || strings.Contains(pe.Message, "quota") {
				pe.Kind = ErrInsufficientQuota
			} else {
				pe.Kind = ErrRateLimit
			}
		case resp.StatusCode == 404:
			if code == "model_not_found" || strings.Contains(pe.Message, "model") {
				pe.Kind = ErrModelNotFound
			} else {
				pe.Kind = ErrInvalidRequest
			}
		case code == "context_length_exceeded" || strings.Contains(pe.Message, "maximum context length"):
			pe.Kind = ErrContextLength
		case code == "content_policy_violation" || strings.Contains(pe.Message, "content policy"):
			pe.Kind = ErrContentFiltered
		case resp.StatusCode == 400:
			pe.Kind = ErrInvalidRequest
		case resp.StatusCode == 529 || resp.StatusCode == 503:
			pe.Kind = ErrOverloaded
		case resp.StatusCode >= 500:
			pe.Kind = ErrServerError
		default:
			pe.Kind = ErrUnknown
		}
		return pe
	}

	// Fallback: no parseable body
	pe.Message = bodyStr
	pe.Kind = classifyByStatus(resp.StatusCode)
	return pe
}

// classifyAnthropicError parses an Anthropic error response and returns a ProviderError.
func classifyAnthropicError(resp *http.Response) *ProviderError {
	body, _ := io.ReadAll(resp.Body)
	bodyStr := string(body)

	pe := &ProviderError{
		Provider:   "anthropic",
		StatusCode: resp.StatusCode,
		Cause:      &HTTPStatusError{StatusCode: resp.StatusCode, Body: bodyStr},
	}

	// Parse Anthropic error envelope: {"type": "error", "error": {"type": "...", "message": "..."}}
	var envelope struct {
		Error struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &envelope) == nil && envelope.Error.Message != "" {
		pe.Message = envelope.Error.Message
		errType := envelope.Error.Type

		switch {
		case errType == "authentication_error" || resp.StatusCode == 401 || resp.StatusCode == 403:
			pe.Kind = ErrAuth
		case errType == "rate_limit_error" || resp.StatusCode == 429:
			if strings.Contains(pe.Message, "quota") || strings.Contains(pe.Message, "credit") {
				pe.Kind = ErrInsufficientQuota
			} else {
				pe.Kind = ErrRateLimit
			}
		case errType == "overloaded_error" || resp.StatusCode == 529:
			pe.Kind = ErrOverloaded
		case errType == "not_found_error" || resp.StatusCode == 404:
			pe.Kind = ErrModelNotFound
		case strings.Contains(pe.Message, "context window") || strings.Contains(pe.Message, "too many tokens") || strings.Contains(pe.Message, "maximum number of tokens"):
			pe.Kind = ErrContextLength
		case strings.Contains(pe.Message, "content policy") || strings.Contains(pe.Message, "safety"):
			pe.Kind = ErrContentFiltered
		case errType == "invalid_request_error" || resp.StatusCode == 400:
			pe.Kind = ErrInvalidRequest
		case resp.StatusCode == 503:
			pe.Kind = ErrOverloaded
		case resp.StatusCode >= 500:
			pe.Kind = ErrServerError
		default:
			pe.Kind = ErrUnknown
		}
		return pe
	}

	pe.Message = bodyStr
	pe.Kind = classifyByStatus(resp.StatusCode)
	return pe
}

// classifyGeminiError converts a Gemini SDK error into a ProviderError.
func classifyGeminiError(err error) *ProviderError {
	pe := &ProviderError{
		Provider: "gemini",
		Cause:    err,
	}

	var apiErr genai.APIError
	if errors.As(err, &apiErr) {
		pe.StatusCode = apiErr.Code
		pe.Message = apiErr.Message

		switch {
		case apiErr.Code == 401 || apiErr.Code == 403:
			pe.Kind = ErrAuth
		case apiErr.Code == 429:
			if strings.Contains(apiErr.Message, "quota") {
				pe.Kind = ErrInsufficientQuota
			} else {
				pe.Kind = ErrRateLimit
			}
		case apiErr.Code == 404:
			pe.Kind = ErrModelNotFound
		case apiErr.Status == "RESOURCE_EXHAUSTED" || strings.Contains(apiErr.Message, "quota"):
			pe.Kind = ErrInsufficientQuota
		case strings.Contains(apiErr.Message, "context window") || strings.Contains(apiErr.Message, "token limit") || strings.Contains(apiErr.Message, "too long"):
			pe.Kind = ErrContextLength
		case strings.Contains(apiErr.Message, "safety") || strings.Contains(apiErr.Message, "blocked") || apiErr.Status == "SAFETY":
			pe.Kind = ErrContentFiltered
		case apiErr.Code == 400:
			pe.Kind = ErrInvalidRequest
		case apiErr.Code == 503 || apiErr.Code == 529:
			pe.Kind = ErrOverloaded
		case apiErr.Code >= 500:
			pe.Kind = ErrServerError
		default:
			pe.Kind = ErrUnknown
		}
		return pe
	}

	// Non-API error (network, etc.)
	pe.Message = err.Error()
	pe.Kind = ErrUnknown
	return pe
}

// classifyByStatus is a fallback when we can't parse the error body.
func classifyByStatus(status int) ErrorKind {
	switch {
	case status == 401 || status == 403:
		return ErrAuth
	case status == 429:
		return ErrRateLimit
	case status == 404:
		return ErrModelNotFound
	case status == 400:
		return ErrInvalidRequest
	case status == 503 || status == 529:
		return ErrOverloaded
	case status >= 500:
		return ErrServerError
	default:
		return ErrUnknown
	}
}
