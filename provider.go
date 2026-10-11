package mail

import (
	"log/slog"

	"github.com/lemmego/api/app"
	"github.com/lemmego/api/config"
	"github.com/lemmego/queue"
	"github.com/lemmego/tasker"
)

// Provider builds the mailer and registers the job queued mail travels on.
//
//	&mail.Provider{
//	    From: mail.Address{Name: "Productshore", Email: "hello@productshore.com"},
//	}
//
// With no Transport set, mail is written to the log rather than sent. That
// default is deliberate: the alternative is a framework that emails real
// people from somebody's laptop the first time a test fixture is seeded. Pass
// a transport from mail/smtp to actually send.
type Provider struct {
	// Transport overrides the configured one. Tests pass mailtest.Recorder.
	Transport Transport
	// Renderer overrides the template renderer.
	Renderer Renderer
	// From is the default sender for a message that sets none.
	From Address
	// Templates is the directory of mail templates. Defaults to
	// resources/mail.
	Templates string
}

func (p *Provider) Provide(a app.App) error {
	templates := p.Templates
	if templates == "" {
		templates = config.MustEnv("MAIL_TEMPLATES", "resources/mail")
	}

	renderer := p.Renderer
	if renderer == nil {
		renderer = NewRenderer(templates)
	}

	from := p.From
	if from.Email == "" {
		from = Address{
			Name:  config.MustEnv("MAIL_FROM_NAME", ""),
			Email: config.MustEnv("MAIL_FROM_ADDRESS", ""),
		}
	}

	transport := p.Transport
	if transport == nil {
		transport = defaultTransport(a)
	}

	mailer := New(transport, renderer, from)
	a.AddService(mailer)
	SetGlobal(mailer)

	// Registered on every process, worker included: a worker needs the
	// factory to rebuild the job, and the same provider list boots both.
	queue.RegisterJob(JobName, func() tasker.Job { return &sendJob{} })
	return nil
}

// defaultTransport returns the log transport, warning in production.
//
// It deliberately does not build an SMTP transport from configuration.
// mail/smtp imports mail, so mail cannot import it back — and the cycle is
// not the only reason: defaulting to a real relay means a misconfigured
// MAIL_HOST is discovered by whoever receives the mail. An application that
// wants to send sets Transport explicitly:
//
//	&mail.Provider{
//	    Transport: smtp.New(smtp.Config{
//	        Host:     config.MustEnv("MAIL_HOST", ""),
//	        Port:     config.MustEnv("MAIL_PORT", 587),
//	        Username: config.MustEnv("MAIL_USERNAME", ""),
//	        Password: config.MustEnv("MAIL_PASSWORD", ""),
//	    }),
//	    From: mail.Address{Name: "Productshore", Email: "hello@productshore.com"},
//	}
func defaultTransport(a app.App) Transport {
	if a.Config().Get("app.env") == "production" {
		slog.Warn("mail: no Transport is configured, so mail is being written to the log instead of sent; " +
			"set Provider.Transport (see mail/smtp)")
	}
	return NewLogTransport()
}
