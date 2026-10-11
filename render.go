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

	var htmlBody bytes.Buffer
	if err := r.html.ExecuteTemplate(&htmlBody, name+".gohtml", data); err != nil {
		return "", "", fmt.Errorf("mail: rendering %s: %w", name, err)
	}

	if r.text != nil {
		var textBody bytes.Buffer
		if err := r.text.ExecuteTemplate(&textBody, name+".gotxt", data); err == nil {
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
}

func (r *embeddedRenderer) Render(name string, data any) (string, string, error) {
	r.once.Do(func() {
		r.html, r.err = template.ParseFS(r.fsys, filepath.Join(r.dir, "*.gohtml"))
	})
	if r.err != nil {
		return "", "", r.err
	}
	var body bytes.Buffer
	if err := r.html.ExecuteTemplate(&body, name+".gohtml", data); err != nil {
		return "", "", fmt.Errorf("mail: rendering %s: %w", name, err)
	}
	return body.String(), TextFromHTML(body.String()), nil
}
