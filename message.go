// Package mail sends one templated email well.
//
// It is deliberately small. A multi-transport abstraction with attachments,
// bounce handling and open tracking is a lot of code to maintain before
// anything has been sent; this sends a rendered message over SMTP, queues it
// through tasker so a slow relay never holds a request open, and stops.
//
//	type Welcome struct {
//	    To   mail.Address
//	    Name string
//	}
//
//	func (m Welcome) Build(ctx context.Context, r mail.Renderer) (*mail.Message, error) {
//	    html, text, err := r.Render("welcome", m)
//	    if err != nil {
//	        return nil, err
//	    }
//	    return &mail.Message{
//	        To:      []mail.Address{m.To},
//	        Subject: "Welcome to Productshore",
//	        HTML:    html,
//	        Text:    text,
//	    }, nil
//	}
//
//	mail.Queue(ctx, Welcome{To: mail.Address{Email: user.Email}, Name: user.Name})
package mail

import (
	"errors"
	"mime"
	"net/mail"
	"strings"
)

// Address is one recipient.
type Address struct {
	Name  string
	Email string
}

// String renders the address for a header, encoding a non-ASCII display name
// as RFC 2047 requires. Without that, a name with an accent in it either
// arrives as mojibake or gets the message rejected.
func (a Address) String() string {
	if a.Name == "" {
		return a.Email
	}
	return (&mail.Address{Name: a.Name, Address: a.Email}).String()
}

// Message is a built email, ready to send.
//
// It carries no attachments and no CC or BCC. Each is easy to add and none of
// them is needed to send a verification link or a weekly digest, which is
// what this exists for.
type Message struct {
	From    Address
	To      []Address
	ReplyTo []Address
	Subject string
	HTML    string
	Text    string
	Headers map[string]string
}

// Validate reports what would make a message unsendable, before a transport
// is dialled.
//
// Both bodies being empty is an error rather than a blank email: an empty
// message is always a bug, and a relay that accepts it has published the bug
// to a real person.
func (m *Message) Validate() error {
	if len(m.To) == 0 {
		return ErrNoRecipient
	}
	for _, to := range m.To {
		if strings.TrimSpace(to.Email) == "" {
			return ErrNoRecipient
		}
		if _, err := mail.ParseAddress(to.Email); err != nil {
			return errors.Join(ErrBadAddress, err)
		}
	}
	if strings.TrimSpace(m.From.Email) == "" {
		return ErrNoSender
	}
	if strings.TrimSpace(m.Subject) == "" {
		return ErrNoSubject
	}
	if strings.TrimSpace(m.HTML) == "" && strings.TrimSpace(m.Text) == "" {
		return ErrNoBody
	}
	return nil
}

// EncodedSubject returns the subject encoded for a header, so a non-ASCII
// subject survives.
func (m *Message) EncodedSubject() string {
	return mime.QEncoding.Encode("utf-8", m.Subject)
}

// Recipients returns the envelope recipients.
func (m *Message) Recipients() []string {
	out := make([]string, 0, len(m.To))
	for _, to := range m.To {
		out = append(out, to.Email)
	}
	return out
}

var (
	ErrNoRecipient = errors.New("mail: the message has no recipient")
	ErrNoSender    = errors.New("mail: the message has no sender; set From or Provider.From")
	ErrNoSubject   = errors.New("mail: the message has no subject")
	ErrNoBody      = errors.New("mail: the message has neither an HTML nor a text body")
	ErrBadAddress  = errors.New("mail: a recipient address is not valid")
	ErrNoMailer    = errors.New("mail: no mailer is configured; register mail.Provider")
)

// Mailable is a message the application describes once, in one place, so the
// subject and the template are not scattered across handlers.
type Mailable interface {
	Build(ctx Context, r Renderer) (*Message, error)
}

// Renderer turns a template name and data into an HTML body and a plain-text
// alternative.
//
// It is passed into Build rather than resolved from a container, so a
// Mailable is a plain value that can be constructed and asserted on in a test
// without an application.
type Renderer interface {
	Render(name string, data any) (html, text string, err error)
}

// Transport delivers a built message.
type Transport interface {
	Send(ctx Context, m *Message) error
}
