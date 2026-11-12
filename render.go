package mail

import (
	"bytes"
	"fmt"
	"html/template"
	"io/fs"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	text "text/template"
)

// templateRenderer renders an HTML body from a directory of templates, and
// derives the plain-text alternative from it unless a .txt template exists.
//
// Deriving text from HTML rather than requiring two templates is a judgement:
// a second template is a second thing to forget to update, and an email whose
// text part has drifted from its HTML part is how a link ends up pointing
// somewhere else for the people whose client shows text. A derived part is
// always consistent. A hand-written one is better, so it wins when present.
type templateRenderer struct {
	dir  string
	once sync.Once
	html *template.Template
	text *text.Template
	err  error

	// One parsed set per locale, built on first use and kept.
	//
	// A single sync.Once cannot serve this: the base set and each locale's
	// are different parses of different globs, and a locale arrives from a
	// request rather than from configuration, so there is no list to walk at
	// boot. sync.Map rather than a mutex and a map because this is read on
	// every send and written once per language.
	locales sync.Map // locale -> *localeSet
}

// localeSet is one language's overlay, parsed once.
type localeSet struct {
	once sync.Once
	html *template.Template
	text *text.Template
	err  error
}

// NewRenderer returns a Renderer over a directory of templates.
//
// Templates are parsed once, lazily, so a project without a mail directory
// boots fine and only fails when it actually tries to send.
func NewRenderer(dir string) Renderer { return &templateRenderer{dir: dir} }

func (r *templateRenderer) load() error {
	r.once.Do(func() {
		htmlGlob := filepath.Join(r.dir, "*.gohtml")
		matches, err := filepath.Glob(htmlGlob)
		if err != nil {
			r.err = err
			return
		}
		if len(matches) == 0 {
			r.err = fmt.Errorf("mail: no templates in %s", r.dir)
			return
		}
		if r.html, r.err = template.ParseGlob(htmlGlob); r.err != nil {
			return
		}
		// Text templates are optional.
		if textMatches, _ := filepath.Glob(filepath.Join(r.dir, "*.gotxt")); len(textMatches) > 0 {
			r.text, _ = text.ParseGlob(filepath.Join(r.dir, "*.gotxt"))
		}
	})
	return r.err
}

func (r *templateRenderer) Render(name string, data any) (string, string, error) {
	if err := r.load(); err != nil {
		return "", "", err
	}
	return render(r.html, r.text, name, data)
}

// RenderIn renders a body from the locale's directory, falling back per
// template to the base one.
//
// The fallback is per *template*, not per language, and that is the point: a
// translator who has done resources/mail/fr/matched.gohtml and not
// fr/verify_email.gohtml should get French for the one and English for the
// other, rather than nothing for either or a boot error for the gap. It falls
// out of how html/template works — the locale's glob is parsed on top of the
// base set, and a later parse of the same template name replaces the earlier
// one, which is documented behaviour rather than an accident.
//
// An unknown locale directory is not an error. A locale is added to the
// supported list before anybody has translated anything, and mail going out in
// the default language is the right behaviour for that week.
func (r *templateRenderer) RenderIn(locale, name string, data any) (string, string, error) {
	if err := r.load(); err != nil {
		return "", "", err
	}

	dir, err := localeDir(r.dir, locale)
	if err != nil {
		return "", "", err
	}

	entry, _ := r.locales.LoadOrStore(locale, &localeSet{})
	set := entry.(*localeSet)
	set.once.Do(func() {
		// Clone, so one language's overlay cannot leak into another's or into
		// the base set. Parsing the locale glob into r.html directly would
		// make the first language rendered the fallback for every other one.
		if set.html, set.err = r.html.Clone(); set.err != nil {
			return
		}
		if _, err := set.html.ParseGlob(filepath.Join(dir, "*.gohtml")); err != nil {
			// No directory, or nothing in it: the base set stands.
			set.html, _ = r.html.Clone()
		}
		if r.text != nil {
			if set.text, _ = r.text.Clone(); set.text != nil {
				if _, err := set.text.ParseGlob(filepath.Join(dir, "*.gotxt")); err != nil {
					set.text, _ = r.text.Clone()
				}
			}
		}
	})
	if set.err != nil {
		return "", "", set.err
	}
	return render(set.html, set.text, name, data)
}

// render executes one template set, preferring a hand-written text part.
func render(html *template.Template, txt *text.Template, name string, data any) (string, string, error) {
	var htmlBody bytes.Buffer
	if err := html.ExecuteTemplate(&htmlBody, name+".gohtml", data); err != nil {
		return "", "", fmt.Errorf("mail: rendering %s: %w", name, err)
	}

	if txt != nil {
		var textBody bytes.Buffer
		if err := txt.ExecuteTemplate(&textBody, name+".gotxt", data); err == nil {
			return htmlBody.String(), textBody.String(), nil
		}
	}
	return htmlBody.String(), TextFromHTML(htmlBody.String()), nil
}

var (
	// Go's regexp is RE2 and has no backreferences, so each element needs
	// its own pattern rather than <(script|style|head)…</\1>.
	dropElements = []*regexp.Regexp{
		regexp.MustCompile(`(?is)<script[^>]*>.*?</script\s*>`),
		regexp.MustCompile(`(?is)<style[^>]*>.*?</style\s*>`),
		regexp.MustCompile(`(?is)<head[^>]*>.*?</head\s*>`),
	}
	lineBreak  = regexp.MustCompile(`(?i)<(br|/p|/div|/h[1-6]|/li|/tr)\s*/?>`)
	anyTag     = regexp.MustCompile(`<[^>]*>`)
	blankLines = regexp.MustCompile(`\n{3,}`)
	spaces     = regexp.MustCompile(`[ \t]{2,}`)
	linkTag    = regexp.MustCompile(`(?is)<a\s[^>]*href=["']([^"']+)["'][^>]*>(.*?)</a>`)
)

// TextFromHTML derives a readable plain-text body from an HTML one.
//
// Links become "text (url)", because a text-only reader who cannot see the
// anchor still needs the address — dropping it is how a verification email
// becomes unusable for exactly the people most likely to be reading text.
func TextFromHTML(html string) string {
	s := html
	for _, pattern := range dropElements {
		s = pattern.ReplaceAllString(s, "")
	}
	s = linkTag.ReplaceAllString(s, "$2 ($1)")
	s = lineBreak.ReplaceAllString(s, "\n")
	s = anyTag.ReplaceAllString(s, "")
	s = template.HTMLEscapeString(s)
	s = strings.NewReplacer(
		"&lt;", "<", "&gt;", ">", "&amp;", "&", "&#34;", `"`, "&#39;", "'", "&nbsp;", " ",
	).Replace(s)

	lines := strings.Split(s, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimSpace(spaces.ReplaceAllString(line, " "))
	}
	s = strings.Join(lines, "\n")
	return strings.TrimSpace(blankLines.ReplaceAllString(s, "\n\n"))
}

// FSRenderer renders from an fs.FS, for templates embedded with go:embed.
func FSRenderer(fsys fs.FS, dir string) Renderer {
	return &embeddedRenderer{fsys: fsys, dir: dir}
}

type embeddedRenderer struct {
	fsys fs.FS
	dir  string
	once sync.Once
	html *template.Template
	err  error

	locales sync.Map // locale -> *localeSet
}

// Note an asymmetry with templateRenderer that predates this file and is left
// as it was found: this renderer has no .gotxt support at all and always
// derives its text part. Giving it one is a change to what an embedded
// renderer sends, which belongs in its own commit rather than smuggled in
// behind a locale feature.

func (r *embeddedRenderer) load() error {
	r.once.Do(func() {
		r.html, r.err = template.ParseFS(r.fsys, filepath.Join(r.dir, "*.gohtml"))
	})
	return r.err
}

func (r *embeddedRenderer) Render(name string, data any) (string, string, error) {
	if err := r.load(); err != nil {
		return "", "", err
	}
	return render(r.html, nil, name, data)
}

func (r *embeddedRenderer) RenderIn(locale, name string, data any) (string, string, error) {
	if err := r.load(); err != nil {
		return "", "", err
	}

	dir, err := localeDir(r.dir, locale)
	if err != nil {
		return "", "", err
	}

	entry, _ := r.locales.LoadOrStore(locale, &localeSet{})
	set := entry.(*localeSet)
	set.once.Do(func() {
		if set.html, set.err = r.html.Clone(); set.err != nil {
			return
		}
		if _, err := set.html.ParseFS(r.fsys, filepath.Join(dir, "*.gohtml")); err != nil {
			set.html, _ = r.html.Clone()
		}
	})
	if set.err != nil {
		return "", "", set.err
	}
	return render(set.html, nil, name, data)
}
