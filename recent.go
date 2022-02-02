package main

import (
	"fmt"
	"html/template"
	"log"
	"net/http"
	"sort"
	"strings"
	"time"

	"cloud.google.com/go/storage"
	"github.com/jehiah/legislator/db"
	"github.com/julienschmidt/httprouter"
)

type RecentLegislation struct {
	File           string
	Name           string
	Date           time.Time // recent change
	Action         string
	StatusName     string
	BodyName       string
	PrimarySponsor db.PersonReference
	NumberSponsors int
}

func (l RecentLegislation) IntroLink() template.URL {
	return template.URL("/" + strings.TrimPrefix(l.File, "Int "))
}
func (l RecentLegislation) IntroLinkText() string {
	return "intro.nyc/" + strings.TrimPrefix(l.File, "Int ")
}

func NewRecentLegislation(l db.Legislation) RecentLegislation {
	r := RecentLegislation{
		File:           l.File,
		Name:           l.Name,
		BodyName:       l.BodyName,
		StatusName:     l.StatusName,
		Date:           l.IntroDate,
		PrimarySponsor: l.Sponsors[0],
		NumberSponsors: len(l.Sponsors),
	}
	// walk in reverse
	for i := len(l.History) - 1; i >= 0; i-- {
		h := l.History[i]
		switch h.Action {
		case "Introduced by Council",
			"Amended by Committee",
			"Approved by Committee",
			"Approved by Council",
			"Vetoed by Mayor",
			"City Charter Rule Adopted":
			r.Action = h.Action
			r.Date = h.Date
			return r
		}
	}
	return r
}

func isSameDate(a, b time.Time) bool {
	y1, m1, d1 := a.In(americaNewYork).Date()
	y2, m2, d2 := b.In(americaNewYork).Date()
	return y1 == y2 && m1 == m2 && d1 == d2
}

type DateGroup struct {
	Date        time.Time
	Legislation []RecentLegislation
}

func NewDateGroups(r []RecentLegislation) []DateGroup {
	var o []DateGroup
	if len(r) == 0 {
		return o
	}
	o = append(o, DateGroup{Date: r[0].Date})
	for _, rr := range r {
		if !isSameDate(rr.Date, o[len(o)-1].Date) {
			o = append(o, DateGroup{Date: rr.Date})
		}
		o[len(o)-1].Legislation = append(o[len(o)-1].Legislation, rr)
	}
	sort.Slice(o, func(i, j int) bool { return o[i].Date.After(o[j].Date) })
	return o
}

// RecentLegislation returns the list of legislation changes /recent
func (a *App) RecentLegislation(w http.ResponseWriter, r *http.Request, ps httprouter.Params) {

	t := newTemplate(a.templateFS, "recent_legislation.html")

	var legislation Legislation

	// TODO: make dyanmic to the current year (w/ fallback to previous year)
	// get all the years for the legislative session
	for year := 2018; year <= 2022; year++ {
		var l []db.Legislation
		err := a.getJSONFile(r.Context(), fmt.Sprintf("build/%d.json", year), &l)
		if err != nil {
			if err == storage.ErrObjectNotExist {
				continue
			}
			log.Print(err)
			http.Error(w, "Internal Server Error", 500)
		}
		legislation.All = append(legislation.All, l...)
	}

	cacheTTL := time.Minute * 30

	type Page struct {
		Page     string
		LastSync LastSync
		Dates    []DateGroup
	}
	body := Page{
		Page:  "recent",
		Dates: NewDateGroups(legislation.Recent(time.Hour * 24 * 14)),
	}

	err := a.getJSONFile(r.Context(), "build/last_sync.json", &body.LastSync)
	if err != nil {
		log.Print(err)
		http.Error(w, "Internal Server Error", 500)
	}

	w.Header().Set("content-type", "text/html")
	a.addExpireHeaders(w, cacheTTL)
	err = t.ExecuteTemplate(w, "recent_legislation.html", body)
	if err != nil {
		log.Print(err)
		http.Error(w, "Internal Server Error", 500)
	}
}
