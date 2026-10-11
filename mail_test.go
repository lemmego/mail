package mail_test

import (
	"context"
	"strings"
	"testing"

	"github.com/lemmego/mail"
	"github.com/lemmego/mail/mailtest"
)

type welcome struct {
	To   mail.Address
	Name string
	Link string
}

func (m welcome) Build(_ mail.Context, r mail.Renderer) (*mail.Message, error) {
	return &mail.Message{
		To:      []mail.Address{m.To},
		Subject: "Welcome to Productshore",
		HTML:    `<p>Hello ` + m.Name + `, <a href="` + m.Link + `">confirm your address</a>.</p>`,
	}, nil
}

func newMailer(recorder *mailtest.Recorder) *mail.Mailer {
	return mail.New(recorder, mail.NewRenderer("testdata/mail"),
		mail.Address{Name: "Productshore", Email: "hello@productshore.com"})
}

func TestSendDeliversTheBuiltMessage(t *testing.T) {
	recorder := mailtest.NewRecorder()
	mailer := newMailer(recorder)

	err := mailer.Send(context.Background(), welcome{
		To:   mail.Address{Name: "Ada Lovelace", Email: "ada@example.com"},
		Name: "Ada",
		Link: "https://productshore.com/verify/abc",
	})
	if err != nil {
		t.Fatal(err)
	}

	if recorder.Count() != 1 {
		t.Fatalf("recorded %d messages, want 1", recorder.Count())
	}
	message := recorder.Last()
	if message.Subject != "Welcome to Productshore" {
		t.Errorf("Subject = %q", message.Subject)
	}
	// The default sender fills in when the mailable sets none, or every
	// mailable has to repeat it.
	if message.From.Email != "hello@productshore.com" {
		t.Errorf("From = %q, want the provider default", message.From.Email)
	}
}

// A message with no recipient, no subject or no body is always a bug. It must
// be refused before a transport is dialled, because a relay that accepts it
// has published the bug to a real person.
func TestUnsendableMessagesAreRefusedBeforeDialling(t *testing.T) {
	for _, tc := range []struct {
		name    string
		message mail.Message
		want    error
	}{
		{"no recipient", mail.Message{Subject: "x", Text: "x"}, mail.ErrNoRecipient},
		{
			"empty recipient",
			mail.Message{To: []mail.Address{{Email: "  "}}, Subject: "x", Text: "x"},
			mail.ErrNoRecipient,
		},
		{
			"invalid recipient",
			mail.Message{To: []mail.Address{{Email: "not-an-address"}}, Subject: "x", Text: "x"},
			mail.ErrBadAddress,
		},
		{
			"no subject",
			mail.Message{To: []mail.Address{{Email: "a@b.com"}}, Text: "x"},
			mail.ErrNoSubject,
		},
		{
			"no body",
			mail.Message{To: []mail.Address{{Email: "a@b.com"}}, Subject: "x"},
			mail.ErrNoBody,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			recorder := mailtest.NewRecorder()
			mailer := newMailer(recorder)

			message := tc.message
			message.From = mail.Address{Email: "hello@productshore.com"}
			if err := mailer.SendMessage(context.Background(), &message); err == nil {
				t.Fatal("an unsendable message was accepted")
			}
			if recorder.Count() != 0 {
				t.Error("the transport was reached despite the message being unsendable")
			}
		})
	}
}

// A message with no sender cannot be delivered, and the provider default is
// the one chance to supply one.
func TestAMessageWithNoSenderAnywhereIsRefused(t *testing.T) {
	recorder := mailtest.NewRecorder()
	mailer := mail.New(recorder, mail.NewRenderer("testdata/mail"), mail.Address{})

	err := mailer.SendMessage(context.Background(), &mail.Message{
		To:      []mail.Address{{Email: "a@b.com"}},
		Subject: "x",
		Text:    "x",
	})
	if err == nil {
		t.Fatal("a message with no sender was accepted")
	}
}

// A non-ASCII display name or subject must be encoded, or it arrives as
// mojibake or gets the message rejected.
func TestNonASCIIHeadersAreEncoded(t *testing.T) {
	address := mail.Address{Name: "José Álvarez", Email: "jose@example.com"}
	if encoded := address.String(); !strings.Contains(encoded, "=?") {
		t.Errorf("Address.String() = %q, want an RFC 2047 encoded name", encoded)
	}

	message := &mail.Message{Subject: "Añadido a la lista"}
	if encoded := message.EncodedSubject(); !strings.Contains(encoded, "=?") {
		t.Errorf("EncodedSubject() = %q, want it encoded", encoded)
	}

	// A plain ASCII name needs no encoding and should not get any.
	plain := mail.Address{Name: "Ada Lovelace", Email: "ada@example.com"}
	if encoded := plain.String(); strings.Contains(encoded, "=?") {
		t.Errorf("an ASCII name was needlessly encoded: %q", encoded)
	}
}

// A text-only reader who cannot see an anchor still needs the address.
// Dropping it is how a verification email becomes unusable for exactly the
// people most likely to be reading text.
func TestTextFromHTMLKeepsLinkTargets(t *testing.T) {
	text := mail.TextFromHTML(
		`<html><head><style>p{color:red}</style></head><body>` +
			`<p>Hello Ada, <a href="https://productshore.com/verify/abc">confirm your address</a>.</p>` +
			`<script>alert(1)</script>` +
			`</body></html>`)

	if !strings.Contains(text, "https://productshore.com/verify/abc") {
		t.Errorf("the link target was dropped:\n%s", text)
	}
	if !strings.Contains(text, "confirm your address") {
		t.Errorf("the link text was dropped:\n%s", text)
	}
	if strings.Contains(text, "color:red") {
		t.Errorf("style content leaked into the text body:\n%s", text)
	}
	if strings.Contains(text, "alert(1)") {
		t.Errorf("script content leaked into the text body:\n%s", text)
	}
	if strings.Contains(text, "<") {
		t.Errorf("markup survived:\n%s", text)
	}
}

func TestTextFromHTMLCollapsesWhitespace(t *testing.T) {
	text := mail.TextFromHTML("<p>one</p>\n\n\n<p>two</p>")
	if strings.Contains(text, "\n\n\n") {
		t.Errorf("runs of blank lines survived: %q", text)
	}
	if !strings.Contains(text, "one") || !strings.Contains(text, "two") {
		t.Errorf("content was lost: %q", text)
	}
}

// The package facade must refuse rather than panic when no provider ran.
func TestTheFacadeReportsAMissingMailer(t *testing.T) {
	previous := mail.Global()
	mail.SetGlobal(nil)
	t.Cleanup(func() { mail.SetGlobal(previous) })

	if err := mail.Send(context.Background(), welcome{}); err != mail.ErrNoMailer {
		t.Errorf("Send with no mailer = %v, want ErrNoMailer", err)
	}
	if err := mail.Queue(context.Background(), welcome{}); err != mail.ErrNoMailer {
		t.Errorf("Queue with no mailer = %v, want ErrNoMailer", err)
	}
}

// The recorder is shared by concurrent sends in a worker test.
func TestRecorderIsSafeForConcurrentSends(t *testing.T) {
	recorder := mailtest.NewRecorder()
	done := make(chan struct{})
	for i := 0; i < 32; i++ {
		go func() {
			defer func() { done <- struct{}{} }()
			_ = recorder.Send(context.Background(), &mail.Message{
				To: []mail.Address{{Email: "a@b.com"}}, Subject: "x", Text: "x",
			})
		}()
	}
	for i := 0; i < 32; i++ {
		<-done
	}
	if recorder.Count() != 32 {
		t.Errorf("recorded %d of 32", recorder.Count())
	}
}

func TestRecorderFiltersByRecipient(t *testing.T) {
	recorder := mailtest.NewRecorder()
	mailer := newMailer(recorder)
	ctx := context.Background()

	for _, email := range []string{"ada@example.com", "grace@example.com", "ada@example.com"} {
		if err := mailer.Send(ctx, welcome{To: mail.Address{Email: email}, Name: "x", Link: "y"}); err != nil {
			t.Fatal(err)
		}
	}

	if got := len(recorder.To("ada@example.com")); got != 2 {
		t.Errorf("To(ada) returned %d, want 2", got)
	}
	// Address matching is case-insensitive, because mailbox comparison in
	// practice is.
	if got := len(recorder.To("ADA@EXAMPLE.COM")); got != 2 {
		t.Errorf("To is case-sensitive; got %d, want 2", got)
	}
}

// A transport failure must reach the caller, not be swallowed.
func TestATransportFailureIsReported(t *testing.T) {
	recorder := mailtest.NewRecorder()
	recorder.Err = context.DeadlineExceeded
	mailer := newMailer(recorder)

	err := mailer.Send(context.Background(), welcome{
		To: mail.Address{Email: "ada@example.com"}, Name: "Ada", Link: "x",
	})
	if err == nil {
		t.Fatal("a transport failure was swallowed")
	}
}
