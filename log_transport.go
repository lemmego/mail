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
	// Provider turns this on outside production, because in development the
	// log *is* the inbox and a verification email without its link is no use
	// to the person who just signed up — they can see that mail happened and
	// not what it said, which looks like a broken feature.
	//
	// Off in production, where this transport should not be in use at all:
	// those links are working credentials, and a log is read by more people
	// and kept far longer than an inbox.
	Bodies bool
}

// NewLogTransport returns a log transport that records only the envelope.
//
// Prefer &LogTransport{Bodies: true} when the log is standing in for an
// inbox, which is what Provider does outside production.
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
