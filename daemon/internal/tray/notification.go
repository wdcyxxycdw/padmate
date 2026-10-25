package tray

import "sync"

// Notification includes a stable key used to suppress repeated warnings.
type Notification struct {
	Key        string
	Title      string
	Body       string
	OncePerRun bool
}

// Text prepares Shell_NotifyIcon's NUL-terminated UTF-16 fields.
func (n Notification) Text() (string, string) {
	return truncateUTF16(clean(n.Title), 63), truncateUTF16(clean(n.Body), 255)
}

// Notifications is a bounded, nonblocking queue. History is bounded too so
// hot-plugging devices or editing many profiles cannot retain unlimited keys.
type Notifications struct {
	mu        sync.Mutex
	queue     chan Notification
	seen      map[string]bool
	history   []string
	permanent int
}

func NewNotifications(capacity int) *Notifications {
	if capacity < 1 {
		capacity = 16
	}
	return &Notifications{queue: make(chan Notification, capacity), seen: make(map[string]bool)}
}

// Add returns true only for a new notice that fits in the queue.
func (q *Notifications) Add(n Notification) bool {
	if q == nil {
		return false
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.seen[n.Key] {
		return false
	}
	if n.OncePerRun && q.permanent >= 16 {
		return false
	}
	select {
	case q.queue <- n:
		if n.OncePerRun {
			q.permanent++
		} else {
			if len(q.history) == 256 {
				delete(q.seen, q.history[0])
				q.history = q.history[1:]
			}
			q.history = append(q.history, n.Key)
		}
		q.seen[n.Key] = true
		return true
	default:
		return false
	}
}

// Take never waits for producers.
func (q *Notifications) Take() (Notification, bool) {
	if q == nil {
		return Notification{}, false
	}
	select {
	case n := <-q.queue:
		return n, true
	default:
		return Notification{}, false
	}
}
