package notify

import (
	"context"
	"regexp"
	"sync"
)

// Message is one captured SMS (Subject empty) or email.
type Message struct {
	To      string
	Subject string
	Body    string
}

// Recorder captures messages in memory instead of sending them. Used by tests.
type Recorder struct {
	mu   sync.Mutex
	msgs []Message
	fail error
}

var (
	_ SMSSender   = (*Recorder)(nil)
	_ EmailSender = (*Recorder)(nil)
)

func NewRecorder() *Recorder { return &Recorder{} }

// FailWith makes subsequent sends return err (nil restores normal behaviour).
func (r *Recorder) FailWith(err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.fail = err
}

func (r *Recorder) SendSMS(_ context.Context, phone, text string) error {
	return r.add(Message{To: phone, Body: text})
}

func (r *Recorder) SendEmail(_ context.Context, to, subject, body string) error {
	return r.add(Message{To: to, Subject: subject, Body: body})
}

func (r *Recorder) add(m Message) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.fail != nil {
		return r.fail
	}
	r.msgs = append(r.msgs, m)
	return nil
}

// Count returns how many messages were sent to the recipient.
func (r *Recorder) Count(to string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, m := range r.msgs {
		if m.To == to {
			n++
		}
	}
	return n
}

var codeRe = regexp.MustCompile(`\b\d{6}\b`)

// LastCode returns the 6-digit code from the latest message to the recipient, or "".
func (r *Recorder) LastCode(to string) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := len(r.msgs) - 1; i >= 0; i-- {
		if r.msgs[i].To == to {
			return codeRe.FindString(r.msgs[i].Body)
		}
	}
	return ""
}
