package notify

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeSMTP is a minimal SMTP server: enough of the protocol for go-mail, with switches for
// the failure modes the sender must survive.
type fakeSMTP struct {
	ext      []string // extra EHLO lines, e.g. "AUTH PLAIN LOGIN"
	hang     bool     // send the greeting, then never answer
	dropQuit bool     // close the connection instead of answering QUIT
	hangQuit bool     // never answer QUIT

	mu    sync.Mutex
	conns []net.Conn
	cmds  []string
	data  string
}

func (f *fakeSMTP) start(t *testing.T) SMTPConfig {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		l.Close()
		f.mu.Lock()
		defer f.mu.Unlock()
		for _, c := range f.conns {
			c.Close() // unblocks a sender still waiting on a hanging server
		}
	})
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			f.mu.Lock()
			f.conns = append(f.conns, c)
			f.mu.Unlock()
			go f.serve(c)
		}
	}()
	return SMTPConfig{Host: "127.0.0.1", Port: l.Addr().(*net.TCPAddr).Port, From: "noreply@example.com", TLS: "none"}
}

func (f *fakeSMTP) serve(c net.Conn) {
	defer c.Close()
	reply := func(s string) { fmt.Fprint(c, s+"\r\n") }
	reply("220 fake ESMTP")
	if f.hang {
		_, _ = io.Copy(io.Discard, c)
		return
	}
	r := bufio.NewReader(c)
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		cmd := strings.ToUpper(strings.TrimSpace(line))
		f.mu.Lock()
		f.cmds = append(f.cmds, cmd)
		f.mu.Unlock()
		switch {
		case strings.HasPrefix(cmd, "EHLO"):
			lines := append([]string{"fake"}, f.ext...)
			for i, l := range lines {
				sep := "-"
				if i == len(lines)-1 {
					sep = " "
				}
				reply("250" + sep + l)
			}
		case strings.HasPrefix(cmd, "AUTH"):
			reply("535 5.7.8 authentication failed")
		case cmd == "DATA":
			reply("354 go ahead")
			var b strings.Builder
			for {
				l, err := r.ReadString('\n')
				if err != nil {
					return
				}
				if l == ".\r\n" {
					break
				}
				b.WriteString(l)
			}
			f.mu.Lock()
			f.data = b.String()
			f.mu.Unlock()
			reply("250 2.0.0 queued")
		case cmd == "QUIT":
			switch {
			case f.hangQuit:
				_, _ = io.Copy(io.Discard, c)
			case !f.dropQuit:
				reply("221 bye")
			}
			return
		default:
			reply("250 OK")
		}
	}
}

func (f *fakeSMTP) sent(prefix string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.cmds {
		if strings.HasPrefix(c, prefix) {
			return true
		}
	}
	return false
}

func newSMTP(t *testing.T, cfg SMTPConfig) *SMTP {
	t.Helper()
	s, err := NewSMTP(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestSMTPSends(t *testing.T) {
	f := &fakeSMTP{}
	s := newSMTP(t, f.start(t))
	err := s.SendEmail(context.Background(), "user@example.com", "Качалка: код\r\nBcc: evil@example.com", "Код: 123456")
	if err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	data := f.data
	f.mu.Unlock()
	if !strings.Contains(data, "123456") {
		t.Fatalf("code missing from the message:\n%s", data)
	}
	if strings.Contains(data, "\nBcc:") {
		t.Fatalf("CRLF in the subject injected a header:\n%s", data)
	}
	if err := s.SendEmail(context.Background(), "a@example.com, evil@example.com", "s", "b"); err == nil {
		t.Fatal("a list of recipients was accepted")
	}
}

func TestSMTPStartTLSIsMandatory(t *testing.T) {
	f := &fakeSMTP{} // does not offer STARTTLS
	cfg := f.start(t)
	cfg.TLS = "starttls"
	if err := newSMTP(t, cfg).SendEmail(context.Background(), "user@example.com", "s", "b"); err == nil {
		t.Fatal("sent without TLS although STARTTLS is required")
	}
	if f.sent("MAIL") {
		t.Fatal("the message went out in clear text")
	}
}

func TestSMTPNoPlainPasswordWithoutTLS(t *testing.T) {
	f := &fakeSMTP{ext: []string{"AUTH PLAIN LOGIN"}}
	cfg := f.start(t)
	cfg.Username, cfg.Password = "user", "secret"
	if err := newSMTP(t, cfg).SendEmail(context.Background(), "user@example.com", "s", "b"); err == nil {
		t.Fatal("sent although the only auth methods expose the password")
	}
	if f.sent("AUTH") {
		t.Fatal("the password was offered over an unencrypted connection")
	}
}

func TestSMTPHonoursContext(t *testing.T) {
	f := &fakeSMTP{hang: true}
	s := newSMTP(t, f.start(t))
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	if err := s.SendEmail(ctx, "user@example.com", "s", "b"); err == nil {
		t.Fatal("no error from a server that never answers")
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("returned after %v, the context allowed 200ms", d)
	}
}

// Once the message is accepted, a broken goodbye must neither fail the send nor delay it.
func TestSMTPFailedQuitIsNotAnError(t *testing.T) {
	for name, f := range map[string]*fakeSMTP{"dropped": {dropQuit: true}, "hanging": {hangQuit: true}} {
		start := time.Now()
		if err := newSMTP(t, f.start(t)).SendEmail(context.Background(), "user@example.com", "s", "b"); err != nil {
			t.Errorf("%s QUIT: message was accepted, but SendEmail returned %v", name, err)
		}
		if d := time.Since(start); d > 2*time.Second {
			t.Errorf("%s QUIT: SendEmail waited %v for the goodbye", name, d)
		}
	}
}
