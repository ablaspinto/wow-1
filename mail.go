package main

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net"
	"net/http"
	"net/mail"
	"net/smtp"
	"net/textproto"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// sendFunc delivers one email. The app logs emails by default; main swaps in SMTP when it's configured.
type sendFunc func(to, subject, text, html string) error

// logMail is the development sender: prints the text version and saves the HTML so you can open it.
func logMail(to, subject, text, html string) error {
	path := filepath.Join(os.TempDir(), "cellar-last-email.html")
	if err := os.WriteFile(path, []byte(html), 0o600); err != nil {
		return err
	}
	log.Printf("email to %s (not sent: set SMTP_HOST to send for real; HTML saved to %s)\nSubject: %s\n\n%s", to, path, subject, text)
	return nil
}

// smtpSender sends through any SMTP provider that supports STARTTLS on port 587 (Amazon SES, Resend,
// Postmark, Mailgun, Gmail with an app password, ...).
func smtpSender(host, port, user, pass, from string) (sendFunc, error) {
	envelope, err := mail.ParseAddress(from)
	if err != nil {
		return nil, fmt.Errorf("MAIL_FROM: %w", err)
	}
	return func(to, subject, text, html string) error {
		msg, err := buildMessage(from, to, subject, text, html)
		if err != nil {
			return err
		}
		conn, err := net.DialTimeout("tcp", net.JoinHostPort(host, port), 10*time.Second)
		if err != nil {
			return err
		}
		defer conn.Close()
		conn.SetDeadline(time.Now().Add(30 * time.Second)) // a stuck mail server must not hang checkout
		c, err := smtp.NewClient(conn, host)
		if err != nil {
			return err
		}
		// Always encrypt and check the server's certificate; never send the password in the clear.
		if err := c.StartTLS(&tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}); err != nil {
			return err
		}
		if user != "" {
			if err := c.Auth(smtp.PlainAuth("", user, pass, host)); err != nil {
				return err
			}
		}
		if err := c.Mail(envelope.Address); err != nil {
			return err
		}
		if err := c.Rcpt(to); err != nil {
			return err
		}
		w, err := c.Data()
		if err != nil {
			return err
		}
		if _, err := w.Write(msg); err != nil {
			return err
		}
		if err := w.Close(); err != nil {
			return err
		}
		return c.Quit()
	}, nil
}

// resendSender sends through Resend's HTTPS API. Use it on hosts that block SMTP ports (Render's free plan).
func resendSender(endpoint, key, from string) (sendFunc, error) {
	if _, err := mail.ParseAddress(from); err != nil {
		return nil, fmt.Errorf("MAIL_FROM: %w", err)
	}
	client := &http.Client{Timeout: 30 * time.Second} // a stuck API must not hang the page
	return func(to, subject, text, html string) error {
		body, err := json.Marshal(map[string]any{"from": from, "to": []string{to}, "subject": subject, "text": text, "html": html})
		if err != nil {
			return err
		}
		req, err := http.NewRequest("POST", endpoint, bytes.NewReader(body))
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+key)
		req.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		if resp.StatusCode/100 != 2 {
			msg, _ := io.ReadAll(io.LimitReader(resp.Body, 500))
			return fmt.Errorf("resend: %s: %s", resp.Status, msg)
		}
		return nil
	}, nil
}

// buildMessage makes a multipart/alternative email: plain text for simple clients, HTML for the rest.
func buildMessage(from, to, subject, text, html string) ([]byte, error) {
	var b bytes.Buffer
	mw := multipart.NewWriter(&b)
	fmt.Fprintf(&b, "From: %s\r\nTo: %s\r\nSubject: %s\r\nDate: %s\r\nMIME-Version: 1.0\r\nContent-Type: multipart/alternative; boundary=%s\r\n\r\n",
		from, (&mail.Address{Address: to}).String(), mime.QEncoding.Encode("utf-8", subject), time.Now().Format(time.RFC1123Z), mw.Boundary())
	for _, part := range []struct{ typ, body string }{{"text/plain", text}, {"text/html", html}} {
		pw, err := mw.CreatePart(textproto.MIMEHeader{
			"Content-Type":              {part.typ + "; charset=utf-8"},
			"Content-Transfer-Encoding": {"quoted-printable"},
		})
		if err != nil {
			return nil, err
		}
		qw := quotedprintable.NewWriter(pw)
		if _, err := qw.Write([]byte(part.body)); err != nil {
			return nil, err
		}
		if err := qw.Close(); err != nil {
			return nil, err
		}
	}
	if err := mw.Close(); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

// mailLimits caps checkout emails, so the form can't be used to flood someone's inbox or burn through the
// email provider's quota (which would also hurt the sender's reputation with spam filters).
// shortcut: kept in memory, so limits reset on restart and aren't shared between servers; move them to
// Redis or the database if the site ever runs on more than one server.
type mailLimits struct {
	mu   sync.Mutex
	sent map[string][]time.Time // "ip:…", "to:…" or "all" -> recent sends
}

var mailRules = []struct {
	n   int
	per time.Duration
}{
	{5, time.Hour},        // per visitor
	{3, time.Hour},        // per recipient
	{100, 24 * time.Hour}, // whole site: stay under free-tier provider limits (Resend: 100/day, Gmail: 500/day)
}

// allow records a send and reports true, or reports false if any limit is reached.
func (l *mailLimits) allow(ip, to string, now time.Time) bool {
	keys := []string{"ip:" + ip, "to:" + strings.ToLower(to), "all"}
	l.mu.Lock()
	defer l.mu.Unlock()
	for i, key := range keys {
		recent := l.sent[key][:0]
		for _, t := range l.sent[key] {
			if now.Sub(t) < mailRules[i].per {
				recent = append(recent, t)
			}
		}
		if len(recent) == 0 {
			delete(l.sent, key) // only keys with recent sends are kept, so the map stays small
		} else {
			l.sent[key] = recent
		}
		if len(recent) >= mailRules[i].n {
			return false
		}
	}
	for _, key := range keys {
		l.sent[key] = append(l.sent[key], now)
	}
	if len(l.sent) > 5000 { // forget visitors and addresses not seen for a day
		for k, ts := range l.sent {
			if now.Sub(ts[len(ts)-1]) > 24*time.Hour {
				delete(l.sent, k)
			}
		}
	}
	return true
}
