package log

import (
	"bytes"
	"context"
	"regexp"
	"sync"
)

// ansiRe matches ANSI SGR escape sequences (colors, bold, reset, etc.)
var ansiRe = regexp.MustCompile(`\x1b\[[0-9;]*m`)

const ringCap = 500

// Broadcaster is an io.Writer that captures log lines, keeps a ring buffer of
// recent entries, and fans out new entries to active subscribers.
type Broadcaster struct {
	mu   sync.Mutex
	ring []string
	head int // next write position
	size int // number of valid entries in ring
	subs map[chan string]struct{}
}

// NewBroadcaster creates a Broadcaster with a ring buffer of ringCap entries.
func NewBroadcaster() *Broadcaster {
	return &Broadcaster{
		ring: make([]string, ringCap),
		subs: make(map[chan string]struct{}),
	}
}

// Write implements io.Writer — captures a log line and fans it out.
func (b *Broadcaster) Write(p []byte) (int, error) {
	line := ansiRe.ReplaceAllString(string(bytes.TrimRight(p, "\n")), "")
	if line == "" {
		return len(p), nil
	}
	b.mu.Lock()
	b.ring[b.head] = line
	b.head = (b.head + 1) % ringCap
	if b.size < ringCap {
		b.size++
	}
	for ch := range b.subs {
		select {
		case ch <- line:
		default: // drop if subscriber is slow
		}
	}
	b.mu.Unlock()
	return len(p), nil
}

// Recent returns the last n log lines (at most ringCap).
func (b *Broadcaster) Recent(n int) []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	if n > b.size {
		n = b.size
	}
	if n == 0 {
		return nil
	}
	result := make([]string, n)
	start := ((b.head - n) + ringCap*2) % ringCap
	for i := 0; i < n; i++ {
		result[i] = b.ring[(start+i)%ringCap]
	}
	return result
}

// Subscribe returns a channel of log lines. The channel is closed when ctx is done.
func (b *Broadcaster) Subscribe(ctx context.Context) <-chan string {
	ch := make(chan string, 64)
	b.mu.Lock()
	b.subs[ch] = struct{}{}
	b.mu.Unlock()
	go func() {
		<-ctx.Done()
		b.mu.Lock()
		delete(b.subs, ch)
		b.mu.Unlock()
		close(ch)
	}()
	return ch
}
