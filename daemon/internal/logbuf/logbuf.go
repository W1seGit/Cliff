package logbuf

import (
	"io"
	"sync"
	"time"
)

const (
	DefaultCapacity = 500
	MaxLineLength   = 4096
)

type entry struct {
	time    time.Time
	message string
}

type Buffer struct {
	mu       sync.Mutex
	entries  []entry
	capacity int
}

func New(capacity int) *Buffer {
	if capacity <= 0 {
		capacity = DefaultCapacity
	}
	return &Buffer{
		entries:  make([]entry, 0, capacity),
		capacity: capacity,
	}
}

func (b *Buffer) append(line string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(line) > MaxLineLength {
		line = line[:MaxLineLength] + "..."
	}
	b.entries = append(b.entries, entry{time: time.Now().UTC(), message: line})
	if len(b.entries) > b.capacity {
		b.entries = b.entries[len(b.entries)-b.capacity:]
	}
}

func (b *Buffer) Lines() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	lines := make([]string, len(b.entries))
	for i, e := range b.entries {
		lines[i] = e.time.Format("2006-01-02 15:04:05") + " " + e.message
	}
	return lines
}

// Writer returns an io.Writer that appends lines to the buffer, splitting on newlines.
func (b *Buffer) Writer() io.Writer {
	return &bufferWriter{buf: b}
}

type bufferWriter struct {
	buf   *Buffer
	rest  []byte
}

func (w *bufferWriter) Write(p []byte) (int, error) {
	w.rest = append(w.rest, p...)
	for {
		nl := -1
		for i, c := range w.rest {
			if c == '\n' {
				nl = i
				break
			}
		}
		if nl < 0 {
			break
		}
		line := string(w.rest[:nl])
		w.rest = w.rest[nl+1:]
		if len(line) > 0 && line[len(line)-1] == '\r' {
			line = line[:len(line)-1]
		}
		w.buf.append(line)
	}
	return len(p), nil
}
