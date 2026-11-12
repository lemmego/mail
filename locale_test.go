package mail_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lemmego/mail"
)

// templates writes a base directory and the locale overlays described.
func templates(t *testing.T, overlays map[string]map[string]string) string {
	t.Helper()
	dir := t.TempDir()

	write := func(path, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	write(filepath.Join(dir, "matched.gohtml"), "<p>You matched with {{.name}}</p>")
	write(filepath.Join(dir, "matched.gotxt"), "You matched with {{.name}}")
	write(filepath.Join(dir, "verify_email.gohtml"), "<p>Confirm your address</p>")

	for locale, files := range overlays {
		for name, body := range files {
			write(filepath.Join(dir, locale, name), body)
		}
	}
	return dir
}

// TestAnUntranslatedTemplateFallsBackOnItsOwn is the behaviour that decides
// whether a second locale is usable before it is finished.
//
// The fallback is per template, not per language: a translator who has done
// fr/matched.gohtml and not fr/verify_email.gohtml gets French for the one and
// English for the other. Per-language fallback would mean one missing file
// reverts a whole language, and a boot error would mean a locale cannot be
// added until everything is translated.
func TestAnUntranslatedTemplateFallsBackOnItsOwn(t *testing.T) {
	dir := templates(t, map[string]map[string]string{
		"fr": {"matched.gohtml": "<p>Vous avez matché avec {{.name}}</p>"},
	})

	renderer, ok := mail.NewRenderer(dir).(mail.LocaleRenderer)
	if !ok {
		t.Fatal("the directory renderer does not implement LocaleRenderer")
	}

	html, _, err := renderer.RenderIn("fr", "matched", map[string]any{"name": "Ada"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(html, "Vous avez matché avec Ada") {
		t.Errorf("the translated body was not used: %q", html)
	}

	html, _, err = renderer.RenderIn("fr", "verify_email", nil)
	if err != nil {
		t.Fatalf("an untranslated template in a translated language must still "+
			"render: %v", err)
	}
	if !strings.Contains(html, "Confirm your address") {
		t.Errorf("the base body was not used for an untranslated template: %q", html)
	}
}

// TestALocaleWithNoDirectoryIsNotAnError, because a locale joins the supported
// list before anybody has translated anything.
func TestALocaleWithNoDirectoryIsNotAnError(t *testing.T) {
	renderer := mail.NewRenderer(templates(t, nil)).(mail.LocaleRenderer)

	html, _, err := renderer.RenderIn("de", "matched", map[string]any{"name": "Ada"})
	if err != nil {
		t.Fatalf("an unknown locale directory should render the base language: %v", err)
	}
	if !strings.Contains(html, "You matched with Ada") {
		t.Errorf("got %q", html)
	}
}

// TestOneLanguageDoesNotLeakIntoAnother is why each overlay is parsed into a
// clone.
//
// Parsing a locale's glob into the shared base set would make whichever
// language rendered first the fallback for every other one — so the second
// reader's untranslated template would arrive in the first reader's language,
// and the symptom would depend on traffic order.
func TestOneLanguageDoesNotLeakIntoAnother(t *testing.T) {
	dir := templates(t, map[string]map[string]string{
		"fr": {"matched.gohtml": "<p>Vous avez matché avec {{.name}}</p>"},
		"de": {"verify_email.gohtml": "<p>Bestätige deine Adresse</p>"},
	})
	renderer := mail.NewRenderer(dir).(mail.LocaleRenderer)

	// French first, then ask German for the template only French translated.
	if _, _, err := renderer.RenderIn("fr", "matched", map[string]any{"name": "Ada"}); err != nil {
		t.Fatal(err)
	}
	html, _, err := renderer.RenderIn("de", "matched", map[string]any{"name": "Ada"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(html, "matché") {
		t.Errorf("the German reader got the French body: %q", html)
	}
	if !strings.Contains(html, "You matched with Ada") {
		t.Errorf("got %q", html)
	}

	// And the reverse order, so the test does not pass by accident of which
	// language was asked first.
	html, _, err = renderer.RenderIn("fr", "verify_email", nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(html, "Bestätige") {
		t.Errorf("the French reader got the German body: %q", html)
	}
}

// TestTheHandWrittenTextPartSurvivesALocale: matched.gotxt exists only in the
// base directory, and a French HTML body must not cost the reader the text
// part the project wrote by hand.
func TestTheHandWrittenTextPartSurvivesALocale(t *testing.T) {
	dir := templates(t, map[string]map[string]string{
		"fr": {"matched.gohtml": "<p>Vous avez matché avec {{.name}}</p>"},
	})
	renderer := mail.NewRenderer(dir).(mail.LocaleRenderer)

	_, txt, err := renderer.RenderIn("fr", "matched", map[string]any{"name": "Ada"})
	if err != nil {
		t.Fatal(err)
	}
	if txt != "You matched with Ada" {
		t.Errorf("text = %q, want the base .gotxt — a translated HTML body must "+
			"not silently drop to a derived text part", txt)
	}
}

// TestALocaleCannotEscapeTheTemplatesDirectory. The value reaches here from a
// cookie or a database column, so "../../etc" is not a language somebody
// forgot to translate.
func TestALocaleCannotEscapeTheTemplatesDirectory(t *testing.T) {
	renderer := mail.NewRenderer(templates(t, nil)).(mail.LocaleRenderer)

	for _, locale := range []string{"../base", "a/b", "..", "."} {
		if _, _, err := renderer.RenderIn(locale, "matched", nil); err == nil {
			t.Errorf("RenderIn(%q) was accepted", locale)
		}
	}
}

// TestTheLocaleIsResolvedBeforeTheQueue is the rule the whole design turns on.
//
// Queue renders on the calling goroutine and enqueues a self-contained
// Message, so a worker has no context, no catalogue and nothing to resolve.
// The language therefore has to be settled at the call, and this asserts it
// against a mailer whose own default is English.
func TestTheLocaleIsResolvedBeforeTheQueue(t *testing.T) {
	dir := templates(t, map[string]map[string]string{
		"fr": {"matched.gohtml": "<p>Vous avez matché avec {{.name}}</p>"},
	})

	recorder := &recordingTransport{}
	mailer := mail.New(recorder, mail.NewRenderer(dir),
		mail.Address{Email: "hello@example.test"},
		mail.WithLocale(func(ctx mail.Context) string {
			locale, _ := ctx.Value(localeKey{}).(string)
			return locale
		}),
	)

	ctx := context.WithValue(context.Background(), localeKey{}, "fr")
	if err := mailer.Send(ctx, matchedMail{name: "Ada"}); err != nil {
		t.Fatal(err)
	}
	if len(recorder.sent) != 1 {
		t.Fatalf("sent %d messages", len(recorder.sent))
	}
	if !strings.Contains(recorder.sent[0].HTML, "Vous avez matché") {
		t.Errorf("the message went out in the wrong language: %q", recorder.sent[0].HTML)
	}

	// And nobody's preference means the default, with no directory consulted.
	if err := mailer.Send(context.Background(), matchedMail{name: "Ada"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(recorder.sent[1].HTML, "You matched with Ada") {
		t.Errorf("got %q", recorder.sent[1].HTML)
	}
}

// TestAMailableNeverSeesTheLocale is what makes this non-breaking.
//
// Build keeps its signature and keeps calling Render with a template name. It
// matters more than it looks: several mailables in this project's own consumer
// discard the context they are handed, so a design expecting Build to read the
// locale would have translated some bodies and silently not others.
func TestAMailableNeverSeesTheLocale(t *testing.T) {
	dir := templates(t, map[string]map[string]string{
		"fr": {"matched.gohtml": "<p>Vous avez matché avec {{.name}}</p>"},
	})

	recorder := &recordingTransport{}
	mailer := mail.New(recorder, mail.NewRenderer(dir),
		mail.Address{Email: "hello@example.test"},
		mail.WithLocale(func(mail.Context) string { return "fr" }),
	)

	// This mailable ignores its context entirely, exactly as the real ones do.
	if err := mailer.Send(context.Background(), contextIgnoringMail{name: "Ada"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(recorder.sent[0].HTML, "Vous avez matché") {
		t.Errorf("a mailable that ignores its context was not translated: %q",
			recorder.sent[0].HTML)
	}
}

type localeKey struct{}

type recordingTransport struct{ sent []*mail.Message }

func (r *recordingTransport) Send(_ mail.Context, m *mail.Message) error {
	r.sent = append(r.sent, m)
	return nil
}

type matchedMail struct{ name string }

func (m matchedMail) Build(_ mail.Context, r mail.Renderer) (*mail.Message, error) {
	html, txt, err := r.Render("matched", map[string]any{"name": m.name})
	if err != nil {
		return nil, err
	}
	return &mail.Message{
		To:      []mail.Address{{Email: "reader@example.test"}},
		Subject: "You matched",
		HTML:    html,
		Text:    txt,
	}, nil
}

type contextIgnoringMail struct{ name string }

func (m contextIgnoringMail) Build(_ mail.Context, r mail.Renderer) (*mail.Message, error) {
	return matchedMail{name: m.name}.Build(nil, r)
}
