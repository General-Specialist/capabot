package api

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"time"
)

// tenantKey is the context key for the tenant ID.
type tenantKey struct{}

// TenantIDFromContext returns the tenant ID embedded by tenantMiddleware,
// or "default" if not present.
func TenantIDFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(tenantKey{}).(string); ok && v != "" {
		return v
	}
	return "default"
}

// tenantMiddleware reads the X-Tenant-ID header and injects the tenant ID into
// the request context. If the header is absent the tenant defaults to "default".
// This enables per-tenant isolation in storage, memory, and rate limiting.
func tenantMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tenantID := r.Header.Get("X-Tenant-ID")
		if tenantID == "" {
			tenantID = "default"
		}
		// Sanitise: strip whitespace and non-printable characters
		tenantID = strings.Map(func(r rune) rune {
			if r < 0x20 || r > 0x7e {
				return -1
			}
			return r
		}, tenantID)
		if tenantID == "" {
			tenantID = "default"
		}
		ctx := context.WithValue(r.Context(), tenantKey{}, tenantID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// authMiddleware rejects requests without a valid Bearer token.
// If apiKey is empty, all requests pass through.
func authMiddleware(apiKey string, next http.Handler) http.Handler {
	if apiKey == "" {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Allow SPA assets and health without auth
		if !strings.HasPrefix(r.URL.Path, "/api/") || r.URL.Path == "/api/health" {
			next.ServeHTTP(w, r)
			return
		}
		auth := r.Header.Get("Authorization")
		token, ok := strings.CutPrefix(auth, "Bearer ")
		if !ok || token != apiKey {
			w.Header().Set("WWW-Authenticate", `Bearer realm="capabot"`)
			writeError(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// rateLimitMiddleware limits API requests using a token-bucket per remote IP.
// rpm = 0 disables rate limiting.
func rateLimitMiddleware(rpm int, next http.Handler) http.Handler {
	if rpm <= 0 {
		return next
	}

	type bucket struct {
		tokens   float64
		lastSeen time.Time
	}

	var mu sync.Mutex
	buckets := make(map[string]*bucket)

	// Refill rate: tokens per nanosecond
	refillRate := float64(rpm) / float64(time.Minute)
	capacity := float64(rpm)

	// Periodically remove stale buckets to prevent memory growth
	go func() {
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			mu.Lock()
			cutoff := time.Now().Add(-10 * time.Minute)
			for ip, b := range buckets {
				if b.lastSeen.Before(cutoff) {
					delete(buckets, ip)
				}
			}
			mu.Unlock()
		}
	}()

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/") {
			next.ServeHTTP(w, r)
			return
		}

		ip := clientIP(r)
		now := time.Now()

		mu.Lock()
		b, exists := buckets[ip]
		if !exists {
			b = &bucket{tokens: capacity, lastSeen: now}
			buckets[ip] = b
		}

		// Refill tokens based on elapsed time
		elapsed := now.Sub(b.lastSeen)
		b.tokens += float64(elapsed) * refillRate
		if b.tokens > capacity {
			b.tokens = capacity
		}
		b.lastSeen = now

		allowed := b.tokens >= 1
		if allowed {
			b.tokens--
		}
		mu.Unlock()

		if !allowed {
			w.Header().Set("Retry-After", "60")
			writeError(w, "rate limit exceeded", http.StatusTooManyRequests)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// clientIP extracts the real client IP, respecting X-Forwarded-For.
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if idx := strings.Index(xff, ","); idx != -1 {
			return strings.TrimSpace(xff[:idx])
		}
		return strings.TrimSpace(xff)
	}
	if xri := r.Header.Get("X-Real-IP"); xri != "" {
		return xri
	}
	// Strip port from RemoteAddr
	addr := r.RemoteAddr
	if idx := strings.LastIndex(addr, ":"); idx != -1 {
		return addr[:idx]
	}
	return addr
}
