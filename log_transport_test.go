package mail_test

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/lemmego/mail"
)

// captureLog installs a JSON logger over a buffer and restores the previous
// default afterwards.
func captureLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	buffer := &bytes.Buffer{}
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(buffer, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(previous) })
	return buffer
}

func verification() *mail.Message {
	return &mail.Message{
		From:    mail.Address{Name: "Productshore", Email: "hello@productshore.test"},
		To:      []mail.Address{{Name: "Ada Lovelace", Email: "ada@example.com"}},
		Subject: "Confirm your email address",
		HTML:    `<a href="https://productshore.test/verify-email/confirm?token=abc123">Confirm</a>`,
		Text:    "Confirm my address https://productshore.test/verify-email/confirm?token=abc123",
	}
}

// TestTheLogTransportAlwaysRecordsTheEnvelope — without this there is no way
// to tell "mail was not configured" from "mail was never sent".
func TestTheLogTransportAlwaysRecordsTheEnvelope(t *testing.T) {
	buffer := captureLog(t)

	if err := mail.NewLogTransport().Send(context.Background(), verification()); err != nil {
		t.Fatal(err)
	}

	logged := buffer.String()
	for _, want := range []string{"ada@example.com", "Confirm your email address", "hello@productshore.test"} {
		if !strings.Contains(logged, want) {
			t.Errorf("the envelope is missing %q from %q", want, logged)
		}
	}
}

// TestBodiesAreLoggedOnlyWhenAsked. The link is the whole point of a
// verification email, and also a working credential, so which side of that
// trade applies has to be a decision and not an accident.
func TestBodiesAreLoggedOnlyWhenAsked(t *testing.T) {
	const link = "token=abc123"

	t.Run("omitted by default", func(t *testing.T) {
		buffer := captureLog(t)
		if err := mail.NewLogTransport().Send(context.Background(), verification()); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(buffer.String(), link) {
			t.Error("a credential reached the log from a transport that was not asked for bodies")
		}
	})

	t.Run("included when asked", func(t *testing.T) {
		buffer := captureLog(t)
		transport := &mail.LogTransport{Bodies: true}
		if err := transport.Send(context.Background(), verification()); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(buffer.String(), link) {
			t.Errorf("Bodies: true did not log the body: %q", buffer.String())
		}
	})
}
