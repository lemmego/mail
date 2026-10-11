package mail

import "log/slog"

// LogTransport writes mail to the application log instead of sending it.
//
// It is the default, and that is a safety decision rather than a convenience
// one: the alternative is a framework that emails real people from somebody's
// laptop the first time a test fixture is seeded.
//
// It lives in this package rather than a subpackage because Provider defaults
// to it, and a subpackage that imports mail cannot be imported by mail.
type LogTransport struct {
	// Bodies includes the rendered bodies in the log.
	//
	// Off by default: a verification or password-reset email contains a
	// working credential, and a log is read by more people and kept for far
	// longer than an inbox. Turn it on while writing a template, not while
	// handling anyone's real address.
	Bodies bool
}

// NewLogTransport returns a log transport.
func NewLogTransport() *LogTransport { return &LogTransport{} }

func (t *LogTransport) Send(_ Context, m *Message) error {
	attrs := []any{
		"from", m.From.String(),
		"to", m.Recipients(),
		"subject", m.Subject,
	}
	if t.Bodies {
		attrs = append(attrs, "html", m.HTML, "text", m.Text)
	}
	slog.Info("mail: not sent (log transport)", attrs...)
	return nil
}
