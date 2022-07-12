package main

import (
	"html/template"
	"io/fs"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/dustin/go-humanize"
	"github.com/gosimple/slug"
)

func commaInt(i int) string {
	return humanize.Comma(int64(i))
}

var nonASCII = regexp.MustCompile(`[^a-z0-9]+`)

func cssClass(s string) string {
	return nonASCII.ReplaceAllString(strings.ToLower(s), "-")
}

func newTemplate(fs fs.FS, n string) *template.Template {
	funcMap := template.FuncMap{
		"ToLower":    strings.ToLower,
		"Comma":      commaInt,
		"Time":       humanize.Time,
		"CSSClass":   cssClass,
		"Slugify":    slug.Make,
		"TrimPrefix": strings.TrimPrefix,
	}
	t := template.New("empty").Funcs(funcMap)
	return template.Must(t.ParseFS(fs, filepath.Join("templates", n), "templates/base.html"))
}
