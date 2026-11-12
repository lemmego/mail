package mail

import (
	"fmt"
	"path"
)

// LocaleRenderer is the optional interface a Renderer implements to render a
// body in a named language.
//
// Optional, and asserted inside this package rather than exposed to a
// Mailable, which is what makes the whole feature non-breaking. Renderer keeps
// its one method, both renderers here gain this one, and an implementation
// outside this module keeps compiling and renders the base language — which
// for a project that has not translated its mail is the right answer anyway.
//
// A locale and a template name rather than a prefixed name, because
// html/template keys a parsed set by base name: "fr/matched.gohtml" and
// "matched.gohtml" are the same template to it, so a decorator that only
// rewrote the name could not express the override at all. The directory has to
// be part of the lookup, not part of the name.
type LocaleRenderer interface {
	RenderIn(locale, name string, data any) (html, text string, err error)
}

// renderIn renders through the locale-aware path when the renderer has one.
//
// An empty locale means nobody expressed a preference, which is not the same
// as preferring the default: looking for a directory named after the default
// language is a wasted stat on every send. That distinction is why
// i18n.LocaleOf returns "" rather than the default locale, and this is the
// caller it was shaped for.
func renderIn(r Renderer, locale string) Renderer {
	if locale == "" {
		return r
	}
	if lr, ok := r.(LocaleRenderer); ok {
		return localeBound{inner: lr, locale: locale, fallback: r}
	}
	return r
}

// localeBound adapts a LocaleRenderer back to a Renderer for one language, so
// a Mailable's Build keeps calling the one method it always called.
type localeBound struct {
	inner    LocaleRenderer
	locale   string
	fallback Renderer
}

func (b localeBound) Render(name string, data any) (string, string, error) {
	return b.inner.RenderIn(b.locale, name, data)
}

// localeDir is where a translated body lives: resources/mail/fr/matched.gohtml
// beside resources/mail/matched.gohtml.
//
// A locale with a path separator in it would escape the templates directory,
// and the locale can come from a cookie or a database column, so it is
// rejected rather than cleaned — a request for "../../etc" is not a language
// anybody failed to translate.
func localeDir(dir, locale string) (string, error) {
	if locale != path.Base(locale) || locale == "." || locale == ".." {
		return "", fmt.Errorf("mail: %q is not a usable locale directory name", locale)
	}
	return path.Join(dir, locale), nil
}
