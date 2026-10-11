// Package mailtest records mail instead of sending it, for tests.
package mailtest

import (
	"context"
	"strings"
	"sync"

	"github.com/lemmego/mail"
)

// Recorder keeps every message it is given.
type Recorder struct {
	mu       sync.Mutex
	messages []*mail.Message
	// Err, when set, is returned by Send, so a test can exercise what
	// happens when the relay refuses.
	Err error
}

// NewRecorder returns an empty recorder.
func NewRecorder() *Recorder { return &Recorder{} }

func (r *Recorder) Send(_ context.Context, m *mail.Message) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.Err != nil {
		return r.Err
	}
	r.messages = append(r.messages, m)
	return nil
}

// Messages returns everything recorded, in order.
func (r *Recorder) Messages() []*mail.Message {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]*mail.Message{}, r.messages...)
}

// Count returns how many messages were recorded.
func (r *Recorder) Count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.messages)
}

// Last returns the most recent message, or nil.
func (r *Recorder) Last() *mail.Message {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.messages) == 0 {
		return nil
	}
	return r.messages[len(r.messages)-1]
}

// Reset forgets everything recorded.
func (r *Recorder) Reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.messages = nil
}

// To returns the messages addressed to an email.
func (r *Recorder) To(email string) []*mail.Message {
	var out []*mail.Message
	for _, m := range r.Messages() {
		for _, recipient := range m.To {
			if strings.EqualFold(recipient.Email, email) {
				out = append(out, m)
				break
			}
		}
	}
	return out
}
