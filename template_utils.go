package main

import (
	"encoding/json"
	"html/template"
	"io/fs"
	"path/filepath"
	"regexp"
	"strings"
	"time"

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

func toJSON(v interface{}) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func newTemplate(fs fs.FS, n string, funcs ...template.FuncMap) *template.Template {
	funcMap := template.FuncMap{
		"ToLower":       strings.ToLower,
		"Comma":         commaInt,
		"Time":          humanize.Time,
		"RFC3339":       func(t time.Time) string { return t.Format(time.RFC3339) },
		"CSSClass":      cssClass,
		"Slugify":       slug.Make,
		"TrimPrefix":    strings.TrimPrefix,
		"toJSON":        toJSON,
		"TrimCommittee": TrimCommittee,
	}
	if len(funcs) > 0 {
		for _, f := range funcs {
			for k, v := range f {
				funcMap[k] = v
			}
		}
	}
	t := template.New("empty").Funcs(funcMap)
	return template.Must(t.ParseFS(fs, filepath.Join("templates", n), "templates/base.html"))
}
