package main

import (
	"context"
	"net/http"

	"golang.org/x/text/language"
	"golang.org/x/text/message"
)

//go:generate gotext -srclang=en update -out=catalog.go -lang=en,ar,bn,zh,fr,ht,ko,pl,ru,es,ur

// func init() {
// 	l := display.Languages(language.English)
// 	for _, t := range tags {
// 		b, _ := t.Base()
// 		fmt.Printf("lang %s base:%s %s name:%s\n", t.String(), b.String(), b.ISO3(), l.Name(b))
// 	}
// }

type i18nMiddleware struct {
	Matcher language.Matcher
	Handler http.Handler
}

func newI18nMiddleware(h http.Handler) *i18nMiddleware {
	return &i18nMiddleware{
		Matcher: language.NewMatcher(message.DefaultCatalog.Languages()),
		Handler: h,
	}
}

const contextKey = "lang"

func Printer(ctx context.Context) *message.Printer {
	return ctx.Value(contextKey).(*message.Printer)
}

func (t *i18nMiddleware) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	query := r.Form.Get("hl")
	cookie, _ := r.Cookie("lang")
	accept := r.Header.Get("Accept-Language")
	fallback := "en"
	tag, _ := language.MatchStrings(t.Matcher, query, cookie.String(), accept, fallback)
	// log.Printf("%#v %s", tag, tag.String())
	printer := message.NewPrinter(tag)
	ctx := context.WithValue(r.Context(), contextKey, printer)
	t.Handler.ServeHTTP(w, r.WithContext(ctx))
}
