package irc

import (
	"sync"
	"time"
)

const (
	// commandCooldown spaces out any two commands from one user.
	commandCooldown = 3 * time.Second
	// multiLineCooldown limits multi-line replies per user and per target.
	multiLineCooldown = 60 * time.Second
	targetCooldown    = 30 * time.Second
	// penaltyNoticeCooldown limits "broadcast into the Grid" replies per user;
	// the penalty itself is always applied.
	penaltyNoticeCooldown = 20 * time.Second
)

// limiter is a keyed cooldown table. It forgets keys once they are stale so a
// stream of distinct users cannot grow it without bound.
type limiter struct {
	mu   sync.Mutex
	last map[string]time.Time
}

func newLimiter() *limiter { return &limiter{last: make(map[string]time.Time)} }

// allow reports whether key may act now, and if so starts its cooldown.
func (l *limiter) allow(key string, cooldown time.Duration, now time.Time) bool {
	return l.allowAll(now, map[string]time.Duration{key: cooldown})
}

// allowAll grants every key or none, so a denied command never burns the
// remaining cooldowns.
func (l *limiter) allowAll(now time.Time, keys map[string]time.Duration) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	for key, cooldown := range keys {
		if last, ok := l.last[key]; ok && now.Sub(last) < cooldown {
			return false
		}
	}
	if len(l.last) > 512 {
		for k, t := range l.last {
			if now.Sub(t) > 10*time.Minute {
				delete(l.last, k)
			}
		}
	}
	for key := range keys {
		l.last[key] = now
	}
	return true
}
