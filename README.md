# lemmego/mail

Transactional email for Lemmego applications: a mailable describes the
message, a renderer builds both bodies, a transport sends it, and `tasker`
takes it off the request.

```go
import "github.com/lemmego/mail"

type Welcome struct{ User *models.User }

func (w Welcome) Build(ctx mail.Context, r mail.Renderer) (*mail.Message, error) {
    html, text, err := r.Render("welcome", w)
    if err != nil {
        return nil, err
    }
    return &mail.Message{
        To:      []mail.Address{{Name: w.User.Name, Email: w.User.Email}},
        Subject: "Welcome to Productshore",
        HTML:    html,
        Text:    text,
    }, nil
}

// in a handler
return mail.Queue(c.RequestContext(), Welcome{User: user})
```

## Install

```bash
go get github.com/lemmego/mail
```

```go
&mail.Provider{
    From: mail.Address{Name: "Productshore", Email: "hello@productshore.com"},
}
```

**With no `Transport` set, mail is written to the log rather than sent.** That
default is deliberate: the alternative is a framework that emails real people
from somebody's laptop the first time a test fixture is seeded. It warns in
production so the default cannot be the reason nothing arrives.

To actually send, pass a transport:

```go
&mail.Provider{
    From: mail.Address{Name: "Productshore", Email: "hello@productshore.com"},
    Transport: smtp.New(smtp.Config{
        Host:     config.MustEnv("MAIL_HOST", ""),
        Port:     config.MustEnv("MAIL_PORT", 587),
        Username: config.MustEnv("MAIL_USERNAME", ""),
        Password: config.MustEnv("MAIL_PASSWORD", ""),
    }),
}
```

The provider does **not** build an SMTP transport from configuration on your
behalf. `mail/smtp` imports `mail`, so `mail` cannot import it back — but the
cycle is not the only reason: defaulting to a real relay means a misconfigured
`MAIL_HOST` is discovered by whoever receives the mail.

## `Send` on the request, `Queue` off it

```go
mail.Send(ctx, mailable)    // delivers on the calling goroutine
mail.Queue(ctx, mailable)   // hands it to tasker
```

**Prefer `Queue` for anything triggered by a request.** An SMTP conversation
is several round trips to someone else's server, and a relay having a slow
minute should not become a slow minute for the person who just signed up.

`Queue` needs `lemmego/queue` registered and a worker running. The provider
registers the job on **every** process, worker included — a worker needs the
factory to rebuild the job, and the same provider list boots both.

`Queue` takes `tasker.DispatchOpt`, so a digest can be deferred or put on its
own queue:

```go
mail.Queue(ctx, digest, tasker.WithDelay(10*time.Minute))
mail.Queue(ctx, digest, tasker.OnQueue("mail"))
```

## Three interfaces

```go
type Mailable interface {
    Build(ctx Context, r Renderer) (*Message, error)
}

type Renderer interface {
    Render(name string, data any) (html, text string, err error)
}

type Transport interface {
    Send(ctx Context, m *Message) error
}
```

A **mailable** describes the message once, in one place, so the subject and
the template are not scattered across handlers.

The renderer is **passed into `Build`** rather than resolved from a container.
That is what makes a mailable a plain value you can construct and assert on in
a test without booting an application.

## Templates

```
resources/mail/
  welcome.html
```

```go
mail.NewRenderer("resources/mail")        // from disk; the default
mail.FSRenderer(embedded, "mail")         // from an embed.FS
```

Override the directory with `Provider.Templates` or `MAIL_TEMPLATES`.

**A plain-text alternative is generated from the HTML** when the mailable does
not supply one, via `mail.TextFromHTML`. It keeps link targets — a text part
reading "click here" with no URL is worse than no text part — and collapses
whitespace, which HTML has plenty of and a text email should not.

## A message is validated before a transport is dialled

```go
func (m *Message) Validate() error
```

| Refused | Why |
|---|---|
| No recipient | Nothing to send to |
| No subject | A blank subject line is always a bug |
| **Neither body** | An empty message is always a bug, and a relay that accepts it has published the bug to a real person |
| Invalid recipient address | Caught here rather than mid-conversation |
| No sender anywhere | Not the mailable's, not the mailer's default |

Validating first means a bug surfaces as an error in your logs rather than as
a connection to a relay that then rejects it, or worse, accepts it.

Non-ASCII headers are RFC 2047 encoded, so a subject with an em dash or a
name with an accent arrives readable rather than as mojibake.

## SMTP configuration

```go
smtp.Config{
    Host:       "smtp.example.com",
    Port:       587,          // default
    Username:   "…",
    Password:   "…",
    Encryption: "starttls",   // "tls" (465), "starttls" (587), "none"
    Timeout:    30 * time.Second,
}
```

`Encryption` is inferred from the port when unset: `tls` for 465, `starttls`
otherwise. `Timeout` bounds the whole conversation, because a relay having a
slow minute must not hold a worker indefinitely.

`InsecureSkipVerify` exists for a local relay with a self-signed certificate
and for nothing else. Against a real relay it makes the TLS pointless, since
an interceptor can present any certificate and read every message.

## Testing

```go
import "github.com/lemmego/mail/mailtest"

recorder := mailtest.NewRecorder()
a := apptest.New(t, apptest.WithProviders(&mail.Provider{Transport: recorder}))

// …trigger the thing that sends…

if recorder.Count() != 1 {
    t.Fatalf("expected one message, got %d", recorder.Count())
}
if got := recorder.To("grace@example.com"); len(got) != 1 {
    t.Fatal("nothing went to grace")
}
```

`Recorder` is safe for concurrent sends, which matters the moment a test
exercises a worker. `Messages`, `Count`, `Last`, `To` and `Reset` are the
whole surface.

`mail.NewLogTransport()` is the other half: it writes the message to the log,
and is what you get by default.

## Deliberate omissions

One transport. No multi-transport routing, no attachments, no notification
abstraction, no bounce handling. None of them is needed to send a verification
link or a weekly digest, which is what this exists for — and each of them
changes the shape of `Message`, so adding one later is a smaller change than
guessing at it now.

## Testing the module

```bash
go test ./...
GOWORK=off go test ./...   # as a published module
```
