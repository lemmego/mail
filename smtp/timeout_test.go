package smtp

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/lemmego/mail"
)

// silentListener accepts connections and then says nothing at all, which is
// what a black-holed port and a wedged relay both look like from here.
func silentListener(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			// Held open deliberately, never written to.
			t.Cleanup(func() { _ = conn.Close() })
		}
	}()
	return ln.Addr().String()
}

func message() *mail.Message {
	return &mail.Message{
		From:    mail.Address{Email: "from@example.test"},
		To:      []mail.Address{{Email: "to@example.test"}},
		Subject: "hello",
		Text:    "hello",
	}
}

func configFor(addr string, encryption string) Config {
	host, port, _ := net.SplitHostPort(addr)
	p := 0
	for _, c := range port {
		p = p*10 + int(c-'0')
	}
	return Config{
		Host: host, Port: p, Encryption: encryption,
		Timeout: 750 * time.Millisecond, InsecureSkipVerify: true,
	}
}

// Timeout says it "bounds the whole conversation", and it bounded nothing
// after the dial: the context was never applied to the socket, so a relay that
// accepted the connection and then stalled held the worker forever. A queued
// send that never returns is worse than one that fails — it occupies a worker
// slot and the job sits in `running` until something requeues it, which on a
// permanently unreachable relay is a loop with no exit.
func TestAStalledRelayDoesNotHoldTheConversationOpen(t *testing.T) {
	transport := New(configFor(silentListener(t), "none"))

	done := make(chan error, 1)
	go func() { done <- transport.Send(context.Background(), message()) }()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("Send succeeded against a relay that never spoke")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Send did not return: the configured timeout does not reach " +
			"the connection, so the conversation is unbounded")
	}
}

// Implicit TLS — the mode Config chooses on its own for port 465 — went
// through tls.DialWithDialer with a bare net.Dialer carrying neither Timeout
// nor Deadline, so neither the connect nor the handshake was bounded by
// anything. The STARTTLS path used DialContext and was fine, which is why this
// only bit the port most people are told to use.
func TestImplicitTLSHonoursTheTimeout(t *testing.T) {
	// Accepts the TCP connection and never completes a handshake.
	transport := New(configFor(silentListener(t), "tls"))

	done := make(chan error, 1)
	go func() { done <- transport.Send(context.Background(), message()) }()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("Send succeeded against a listener that never handshook")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the TLS handshake is unbounded: tls.DialWithDialer was given " +
			"a dialer with no deadline, so Timeout never applied to port 465")
	}
}

// A platform that blocks outbound SMTP blocks 25, 465 and 587, so a relay
// publishes 2465 and 2587 for people deploying there — and those are what
// somebody reaches for after a dial to 465 times out. The rule knew only 465,
// so 2465 was handed STARTTLS: a plaintext EHLO into a socket waiting for a
// TLS ClientHello, which does not refuse, it hangs — indistinguishable from
// the blocked port that sent you there.
func TestTheAlternateSubmissionPortsPickTheRightMode(t *testing.T) {
	for port, want := range map[int]string{
		465:  "tls",
		2465: "tls",
		587:  "starttls",
		2587: "starttls",
		25:   "starttls",
	} {
		if got := New(Config{Host: "relay.test", Port: port}).cfg.Encryption; got != want {
			t.Errorf("port %d resolved to %q, want %q", port, got, want)
		}
	}
}

// An explicit choice still wins over the guess.
func TestAnExplicitEncryptionIsNotOverridden(t *testing.T) {
	if got := New(Config{Host: "relay.test", Port: 465, Encryption: "none"}).cfg.Encryption; got != "none" {
		t.Errorf("Encryption = %q, want none", got)
	}
}
