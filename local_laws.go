package main

import (
	"fmt"
	"html/template"
	"log"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jehiah/legislator/legistar"
)

type LocalLaw struct {
	File, LocalLaw, Title string
}

func (ll LocalLaw) TitleShort() string {
	if i := strings.Index(ll.Title, "thoroughfares and public places"); i > 0 {
		return ll.Title[:i+len("thoroughfares and public places")]
	}
	return ll.Title
}

func (ll LocalLaw) IntroLink() template.URL {
	f := strings.TrimPrefix(ll.File, "Int ")
	// some older entries have "Int 0349-1998-A"
	if strings.Count(f, "-") == 2 {
		f = strings.Join(strings.Split(f, "-")[:2], "-")
	}
	return template.URL("/" + f)
}
func (ll LocalLaw) LocalLawLink() template.URL {
	return template.URL("/local-laws/" + fmt.Sprintf("%d-%d", ll.Year(), ll.LocalLawNumber()))
}
func (ll LocalLaw) IntroLinkText() string {
	return "intro.nyc" + string(ll.IntroLink())
}
func (ll LocalLaw) Year() int {
	c := strings.Split(ll.LocalLaw, "/")
	if len(c) == 2 {
		n, _ := strconv.Atoi(c[0])
		return n
	}
	return 0
}
func (ll LocalLaw) LocalLawNumber() int {
	c := strings.Split(ll.LocalLaw, "/")
	if len(c) == 2 {
		n, _ := strconv.Atoi(c[1])
		return n
	}
	return 0
}

type LocalLawYear struct {
	Year int
	Laws []LocalLaw
}

func groupLaws(l []LocalLaw) []LocalLawYear {
	years := make(map[int]*LocalLawYear)
	for _, ll := range l {
		g, ok := years[ll.Year()]
		if !ok {
			g = &LocalLawYear{Year: ll.Year()}
			years[ll.Year()] = g
		}
		g.Laws = append(g.Laws, ll)
	}
	var groups []LocalLawYear
	for _, g := range years {
		sort.Slice(g.Laws, func(i, j int) bool { return g.Laws[i].LocalLawNumber() < g.Laws[j].LocalLawNumber() })
		groups = append(groups, *g)
	}
	sort.Slice(groups, func(i, j int) bool { return groups[i].Year > groups[j].Year })
	return groups
}

// LocalLaws returns the list of local laws at /local-laws
// and handles /local-laws/2024
// and handles /local-laws/2024-102
func (a *App) LocalLaws(w http.ResponseWriter, r *http.Request) {
	path := r.PathValue("year")
	ctx := r.Context()
	if matched, _ := regexp.MatchString("^(19|20)[9012][0-9]-[0-9]{1,3}$", path); matched {
		c := strings.Split(path, "-")
		n, _ := strconv.Atoi(c[1])
		filter := legistar.AndFilters(
			legistar.MatterTypeFilter("Introduction"),
			legistar.MatterEnactmentNumberFilter(fmt.Sprintf("%s/%03d", c[0], n)),
		)

		matters, err := a.legistar.Matters(ctx, filter)
		if err != nil {
			log.Print(err)
			http.Error(w, "unknown error", 500)
			return
		}
		if len(matters) != 1 {
			// TODO: cache?
			http.Error(w, "Not Found", 404)
			return
		}

		attachments, err := a.legistar.MatterAttachments(ctx, matters[0].ID)
		if err != nil {
			log.Print(err)
			http.Error(w, "unknown error", 500)
			return
		}
		for _, attachment := range attachments {
			if strings.HasPrefix(attachment.Name, "Local Law") {
				a.addExpireHeaders(w, time.Hour*24*7)
				http.Redirect(w, r, attachment.Link, 301)
				return
			}
		}

		// no attachment - redirect to the legislation page
		redirect, err := a.legistar.LookupWebURL(r.Context(), matters[0].ID)
		if err != nil {
			log.Print(err)
			http.Error(w, "unknown error", 500)
			return
		}
		// a.cachedRedirects[file] = redirect
		a.addExpireHeaders(w, time.Hour)
		http.Redirect(w, r, redirect, 302)

		return
	}

	t := newTemplate(a.templateFS, "local_laws.html")
	T := Printer(r.Context())

	var laws []LocalLaw
	err := a.getJSONFile(r.Context(), "build/local_laws.json", &laws)
	if err != nil {
		log.Print(err)
		http.Error(w, "Internal Server Error", 500)
		return
	}

	g := groupLaws(laws)

	if path == "" {
		a.addExpireHeaders(w, time.Hour)
		http.Redirect(w, r, fmt.Sprintf("/local-laws/%d", g[0].Year), 302)
		return
	}
	cacheTTL := time.Minute * 30
	if path != strconv.Itoa(time.Now().Year()) {
		cacheTTL = time.Hour * 24
	}
	var localLaw LocalLawYear
	for _, gg := range g {
		if strconv.Itoa(gg.Year) == path {
			localLaw = gg
			break
		}
	}

	type Page struct {
		Page string
		LocalLawYear
		All      []LocalLawYear
		LastSync LastSync
		Title    string
	}
	body := Page{
		Page:         "local-laws",
		Title:        T.Sprintf("NYC Local Laws of %d", localLaw.Year),
		LocalLawYear: localLaw,
		All:          g,
	}
	if body.LocalLawYear.Year == 0 {
		http.Error(w, "Not Found", 404)
		return
	}

	err = a.getJSONFile(r.Context(), "build/last_sync.json", &body.LastSync)
	if err != nil {
		log.Print(err)
		http.Error(w, "Internal Server Error", 500)
		return
	}

	w.Header().Set("content-type", "text/html")
	a.addExpireHeaders(w, cacheTTL)
	err = t.ExecuteTemplate(w, "local_laws.html", body)
	if err != nil {
		log.Print(err)
		http.Error(w, "Internal Server Error", 500)
		return
	}
}

// LocalLaw redirects to the attachment with name "Local Law ..."
// URL: /1234-2020/local-law
//
// file == 1234-2020
func (a *App) LocalLaw(w http.ResponseWriter, r *http.Request) {
	file := r.PathValue("file")
	ctx := r.Context()
	if !IsValidFileNumber(file) {
		http.Error(w, "Not Found", 404)
		return
	}
	file = fmt.Sprintf("Int %s", file)

	filter := legistar.AndFilters(
		legistar.MatterTypeFilter("Introduction"),
		legistar.MatterFileFilter(file),
	)

	matters, err := a.legistar.Matters(ctx, filter)
	if err != nil {
		log.Print(err)
		http.Error(w, "unknown error", 500)
		return
	}
	if len(matters) != 1 {
		// TODO: cache?
		http.Error(w, "Not Found", 404)
		return
	}
	attachments, err := a.legistar.MatterAttachments(ctx, matters[0].ID)
	if err != nil {
		log.Print(err)
		http.Error(w, "unknown error", 500)
		return
	}
	for _, attachment := range attachments {
		if strings.HasPrefix(attachment.Name, "Local Law") {
			a.addExpireHeaders(w, time.Hour*24*7)
			http.Redirect(w, r, attachment.Link, 301)
			return
		}
	}
	http.Error(w, "Not Found", 404)
	return
}
