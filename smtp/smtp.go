// Package smtp delivers mail over SMTP.
package smtp

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"mime/quotedprintable"
	"net"
	"net/smtp"
	"strings"
	"time"

	"github.com/lemmego/mail"
)

// Config describes the relay.
type Config struct {
	Host     string
	Port     int
	Username string
	Password string
	// Encryption is "tls" for implicit TLS (port 465), "starttls" to upgrade
	// an open connection (587), or "none".
	Encryption string
	// Timeout bounds the whole conversation. A relay having a slow minute
	// must not hold a worker indefinitely.
	Timeout time.Duration
	// InsecureSkipVerify disables certificate verification. It exists for a
	// local relay with a self-signed certificate and for nothing else —
	// against a real relay it makes the TLS pointless, since an interceptor
	// can present any certificate and read every message.
	InsecureSkipVerify bool
}

// Transport sends over SMTP.
type Transport struct{ cfg Config }

// New returns an SMTP transport.
func New(cfg Config) *Transport {
	if cfg.Port == 0 {
		cfg.Port = 587
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 30 * time.Second
	}
	if cfg.Encryption == "" {
		if cfg.Port == 465 {
			cfg.Encryption = "tls"
		} else {
			cfg.Encryption = "starttls"
		}
	}
	return &Transport{cfg: cfg}
}

// ErrNoHost means the transport was built with no relay to talk to.
var ErrNoHost = errors.New("smtp: no host is configured")

func (t *Transport) Send(ctx context.Context, m *mail.Message) error {
	if t.cfg.Host == "" {
		return ErrNoHost
	}
	if err := m.Validate(); err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(ctx, t.cfg.Timeout)
	defer cancel()

	client, err := t.dial(ctx)
	if err != nil {
		return err
	}
	defer client.Close()

	if t.cfg.Encryption == "starttls" {
		if ok, _ := client.Extension("STARTTLS"); ok {
			if err := client.StartTLS(&tls.Config{
				ServerName:         t.cfg.Host,
				InsecureSkipVerify: t.cfg.InsecureSkipVerify,
			}); err != nil {
				return fmt.Errorf("smtp: starttls: %w", err)
			}
		} else {
			// Refuse rather than fall back to plaintext. A relay that has
			// stopped advertising STARTTLS is either misconfigured or being
			// intercepted, and sending the credentials anyway is how they
			// leak.
			return errors.New("smtp: the relay does not offer STARTTLS; refusing to send credentials in the clear")
		}
	}

	if t.cfg.Username != "" {
		auth := smtp.PlainAuth("", t.cfg.Username, t.cfg.Password, t.cfg.Host)
		if err := client.Auth(auth); err != nil {
			return fmt.Errorf("smtp: authenticating: %w", err)
		}
	}

	if err := client.Mail(m.From.Email); err != nil {
		return fmt.Errorf("smtp: MAIL FROM: %w", err)
	}
	for _, recipient := range m.Recipients() {
		if err := client.Rcpt(recipient); err != nil {
			return fmt.Errorf("smtp: RCPT TO %s: %w", recipient, err)
		}
	}

	writer, err := client.Data()
	if err != nil {
		return fmt.Errorf("smtp: DATA: %w", err)
	}
	if _, err := writer.Write(render(m)); err != nil {
		return fmt.Errorf("smtp: writing the message: %w", err)
	}
	if err := writer.Close(); err != nil {
		return fmt.Errorf("smtp: closing the message: %w", err)
	}
	return client.Quit()
}

// dial opens the connection, bounded by the context in both encryption modes.
//
// Two things used to escape the budget. tls.DialWithDialer took a bare
// net.Dialer carrying neither Timeout nor Deadline, so implicit TLS — the
// mode Config picks on its own for port 465 — had no limit on the connect or
// the handshake; only the STARTTLS branch, which used DialContext, was
// bounded. And nothing ever put a deadline on the socket, so once a relay
// accepted the connection the conversation ran without a clock in either
// mode.
//
// Both matter in the same place. net/smtp has no context support, so a
// deadline on the connection is the only thing that can stop a stalled
// exchange, and a queued send that never returns is worse than one that
// fails: it holds a worker slot and leaves the job in `running` until
// something requeues it, which against an unreachable relay is a loop with no
// exit. The deadline covers the whole exchange rather than each read,
// deliberately — that is what the Timeout field says it does.
func (t *Transport) dial(ctx context.Context) (*smtp.Client, error) {
	address := net.JoinHostPort(t.cfg.Host, fmt.Sprint(t.cfg.Port))
	dialer := &net.Dialer{}
	deadline, bounded := ctx.Deadline()
	if bounded {
		dialer.Deadline = deadline
	}

	var conn net.Conn
	var err error
	if t.cfg.Encryption == "tls" {
		conn, err = tls.DialWithDialer(dialer, "tcp", address, &tls.Config{
			ServerName:         t.cfg.Host,
			InsecureSkipVerify: t.cfg.InsecureSkipVerify,
		})
	} else {
		conn, err = dialer.DialContext(ctx, "tcp", address)
	}
	if err != nil {
		return nil, fmt.Errorf("smtp: dialing %s: %w", address, err)
	}

	if bounded {
		if err := conn.SetDeadline(deadline); err != nil {
			_ = conn.Close()
			return nil, fmt.Errorf("smtp: setting the deadline on %s: %w", address, err)
		}
	}
	return smtp.NewClient(conn, t.cfg.Host)
}

// render builds the MIME message.
//
// Both bodies go out as a multipart/alternative when both exist, so a client
// that cannot render HTML still gets something readable. Quoted-printable
// encoding keeps a non-ASCII body intact and avoids the line-length limit
// that would otherwise mangle a long URL — which, in a verification email, is
// the one thing that must survive.
func render(m *mail.Message) []byte {
	var b strings.Builder

	b.WriteString("From: " + m.From.String() + "\r\n")
	b.WriteString("To: " + joinAddresses(m.To) + "\r\n")
	if len(m.ReplyTo) > 0 {
		b.WriteString("Reply-To: " + joinAddresses(m.ReplyTo) + "\r\n")
	}
	b.WriteString("Subject: " + m.EncodedSubject() + "\r\n")
	b.WriteString("Date: " + time.Now().Format(time.RFC1123Z) + "\r\n")
	b.WriteString("MIME-Version: 1.0\r\n")
	for key, value := range m.Headers {
		b.WriteString(key + ": " + value + "\r\n")
	}

	switch {
	case m.HTML != "" && m.Text != "":
		boundary := "lemmego-" + fmt.Sprint(time.Now().UnixNano())
		b.WriteString("Content-Type: multipart/alternative; boundary=\"" + boundary + "\"\r\n\r\n")
		// Text first: a client picks the last part it understands, so the
		// HTML has to come second to be preferred.
		writePart(&b, boundary, "text/plain; charset=utf-8", m.Text)
		writePart(&b, boundary, "text/html; charset=utf-8", m.HTML)
		b.WriteString("--" + boundary + "--\r\n")
	case m.HTML != "":
		b.WriteString("Content-Type: text/html; charset=utf-8\r\n")
		b.WriteString("Content-Transfer-Encoding: quoted-printable\r\n\r\n")
		b.WriteString(quoted(m.HTML))
	default:
		b.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
		b.WriteString("Content-Transfer-Encoding: quoted-printable\r\n\r\n")
		b.WriteString(quoted(m.Text))
	}

	return []byte(b.String())
}

func writePart(b *strings.Builder, boundary, contentType, body string) {
	b.WriteString("--" + boundary + "\r\n")
	b.WriteString("Content-Type: " + contentType + "\r\n")
	b.WriteString("Content-Transfer-Encoding: quoted-printable\r\n\r\n")
	b.WriteString(quoted(body))
	b.WriteString("\r\n")
}

func quoted(body string) string {
	var out strings.Builder
	writer := quotedprintable.NewWriter(&out)
	_, _ = writer.Write([]byte(body))
	_ = writer.Close()
	return out.String()
}

func joinAddresses(addresses []mail.Address) string {
	parts := make([]string, 0, len(addresses))
	for _, address := range addresses {
		parts = append(parts, address.String())
	}
	return strings.Join(parts, ", ")
}
