package mail

import (
	"fmt"

	"github.com/lemmego/api/app"
	"github.com/lemmego/queue"
	"github.com/lemmego/tasker"
)

// JobName is the registry key for queued mail.
//
// tasker derives a job's Kind from fmt.Sprintf("%T", job), so the key is the
// type string rather than a friendly name. Stating it as a constant keeps the
// registration and the dispatch from drifting apart.
var JobName = fmt.Sprintf("%T", &sendJob{})

// Mailer builds and sends mailables.
type Mailer struct {
	transport Transport
	renderer  Renderer
	from      Address
	locale    func(Context) string
}

// Option configures a Mailer.
type Option func(*Mailer)

// WithLocale tells the mailer how to find the language a message should be
// written in.
//
// The signature is func(context.Context) string and nothing more, which is
// what keeps this module free of any translator: i18n.LocaleOf satisfies it
// structurally, and mail never learns what a locale is beyond "the name of a
// directory under the templates directory". An empty answer means nobody
// expressed a preference, which is not the same as preferring the default —
// so nothing is looked up and no stat is wasted.
//
// It is read on the calling goroutine, inside Send and Queue, which is the
// only place it can be: Queue renders eagerly and enqueues a self-contained
// Message, so a worker has no context, no catalogue and nothing to resolve.
// Resolving a locale in a job handler is the mistake this forecloses.
func WithLocale(f func(Context) string) Option {
	return func(m *Mailer) { m.locale = f }
}

// New returns a Mailer. Most applications get one from Provider.
func New(transport Transport, renderer Renderer, from Address, opts ...Option) *Mailer {
	m := &Mailer{transport: transport, renderer: renderer, from: from}
	for _, opt := range opts {
		opt(m)
	}
	return m
}

// Send builds the mailable and delivers it on the calling goroutine.
//
// Prefer Queue for anything triggered by a request. An SMTP conversation is
// several round trips to someone else's server, and a relay having a slow
// minute should not become a slow minute for the person who just signed up.
func (m *Mailer) Send(ctx Context, mb Mailable) error {
	message, err := m.build(ctx, mb)
	if err != nil {
		return err
	}
	return m.transport.Send(ctx, message)
}

// SendMessage delivers an already-built message.
func (m *Mailer) SendMessage(ctx Context, message *Message) error {
	m.applyFrom(message)
	if err := message.Validate(); err != nil {
		return err
	}
	return m.transport.Send(ctx, message)
}

// Queue builds the mailable now and enqueues the rendered message.
//
// Building eagerly is a deliberate trade. The job payload is a self-contained
// Message, so the worker needs none of the application's types and there is no
// registry mapping a job name back to a Mailable — which is the part of a
// queued-mail design that usually rots. The cost is that a message queued
// before an edit carries the pre-edit content; for transactional mail that is
// what you want anyway, since the email should say what was true when it was
// triggered.
//
// It also means a template error surfaces in the request that caused it,
// where someone can see it, instead of in a worker log an hour later.
func (m *Mailer) Queue(ctx Context, mb Mailable, opts ...tasker.DispatchOpt) error {
	message, err := m.build(ctx, mb)
	if err != nil {
		return err
	}
	_, err = queue.Dispatch(ctx, &sendJob{Message: message}, opts...)
	return err
}

func (m *Mailer) build(ctx Context, mb Mailable) (*Message, error) {
	if mb == nil {
		return nil, fmt.Errorf("mail: nil mailable")
	}
	message, err := mb.Build(ctx, m.rendererFor(ctx))
	if err != nil {
		return nil, err
	}
	if message == nil {
		return nil, fmt.Errorf("mail: %T built a nil message", mb)
	}
	m.applyFrom(message)
	if err := message.Validate(); err != nil {
		return nil, err
	}
	return message, nil
}

// rendererFor binds this send's language to the renderer the Mailable gets.
//
// The Mailable is handed a plain Renderer and keeps calling Render with a
// template name, exactly as before — which matters more than it looks:
// several mailables in this repository's own consumer discard the context
// they are given, so a design that expected a Build to read the locale itself
// would have translated some bodies and silently not others.
func (m *Mailer) rendererFor(ctx Context) Renderer {
	if m.locale == nil {
		return m.renderer
	}
	return renderIn(m.renderer, m.locale(ctx))
}

func (m *Mailer) applyFrom(message *Message) {
	if message.From.Email == "" {
		message.From = m.from
	}
}

// Transport returns the configured transport, so a test can assert against a
// recorder without reaching into the container twice.
func (m *Mailer) Transport() Transport { return m.transport }

// sendJob carries a rendered message to a worker.
//
// tasker keys the registry on the job's Go type string, so this is registered
// under JobName — which is that string, not a name of our choosing.
type sendJob struct {
	Message *Message `json:"message"`
}

// Handle delivers the message through the process's mailer rather than one
// captured at dispatch time. A worker is a different process from the one
// that queued the job, so there is nothing to capture; it reads the mailer the
// worker's own provider installed.
func (j *sendJob) Handle(ctx Context) error {
	mailer := globalMailer()
	if mailer == nil {
		return ErrNoMailer
	}
	if j.Message == nil {
		return ErrNoBody
	}
	return mailer.transport.Send(ctx, j.Message)
}

// ---------------------------------------------------------------------------
// Package facade
//
// A package-level mailer matches cache and queue, so a service can send
// without threading a *Mailer through every constructor.

var global *Mailer

// SetGlobal installs the mailer the package functions use. Provider calls it.
func SetGlobal(m *Mailer) { global = m }

// Global returns the installed mailer, so a test can save and restore it.
func Global() *Mailer { return global }

func globalMailer() *Mailer { return global }

// Send builds and delivers a mailable on the calling goroutine.
func Send(ctx Context, mb Mailable) error {
	if global == nil {
		return ErrNoMailer
	}
	return global.Send(ctx, mb)
}

// Queue builds a mailable and enqueues it.
func Queue(ctx Context, mb Mailable, opts ...tasker.DispatchOpt) error {
	if global == nil {
		return ErrNoMailer
	}
	return global.Queue(ctx, mb, opts...)
}

// Get returns the application's mailer.
func Get(a app.App) (*Mailer, bool) { return app.Lookup[*Mailer](a) }
