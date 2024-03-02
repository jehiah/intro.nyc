package main

import (
	"fmt"
	"html/template"
	"log"
	"net/http"
	"os"
	"sort"
	"strconv"
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

func (r RecentLegislation) Number() int {
	c := strings.Split(strings.TrimPrefix(r.File, "Int "), "-")
	if len(c) == 2 {
		n, _ := strconv.Atoi(c[0])
		return n
	}
	return 0
}

func (l RecentLegislation) IntroLink() template.URL {
	return template.URL("/" + strings.TrimPrefix(l.File, "Int "))
}
func (l RecentLegislation) IntroLinkText() string {
	return "intro.nyc/" + strings.TrimPrefix(l.File, "Int ")
}

func NewRecentLegislation(l Legislation) RecentLegislation {
	r := RecentLegislation{
		File:           l.File,
		Name:           l.Name,
		BodyName:       l.BodyName,
		StatusName:     l.StatusName,
		Date:           l.IntroDate,
		PrimarySponsor: l.PrimarySponsor(),
		NumberSponsors: len(l.Sponsors),
	}
	r.Action, r.Date = l.RecentAction()
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

func (d DateGroup) IsFuture() bool {
	return d.Date.After(time.Now())
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
	for _, g := range o {
		sort.Slice(g.Legislation, func(i, j int) bool { return g.Legislation[i].Number() < g.Legislation[j].Number() })
	}
	return o
}

// RecentLegislation returns the list of legislation changes /recent
func (a *App) RecentLegislation(w http.ResponseWriter, r *http.Request, ps httprouter.Params) {

	t := newTemplate(a.templateFS, "recent_legislation.html")

	type Page struct {
		Page           string
		LastSync       LastSync
		Dates          []DateGroup
		ResubmitLookup map[string]*Legislation
	}
	body := Page{
		Page:           "recent",
		ResubmitLookup: make(map[string]*Legislation),
	}

	var legislation LegislationList

	// get all the years for the legislative session
	for year := CurrentSession.StartYear; year <= CurrentSession.EndYear && year <= time.Now().Year(); year++ {
		var l []Legislation
		err := a.getJSONFile(r.Context(), fmt.Sprintf("build/%d.json", year), &l)
		if err != nil {
			if err == storage.ErrObjectNotExist || os.IsNotExist(err) {
				continue
			}
			log.Print(err)
			http.Error(w, "Internal Server Error", 500)
			return
		}
		legislation = append(legislation, l...)
	}
	body.Dates = NewDateGroups(legislation.Recent(time.Hour * 24 * 30))

	// build a lookup of re-submit bills
	for year := CurrentSession.StartYear; year <= CurrentSession.EndYear && year <= time.Now().Year(); year++ {
		var resubmitFile ResubmitFile
		err := a.getJSONFile(r.Context(), fmt.Sprintf("build/resubmit_%d.json", year), &resubmitFile)
		if err != nil {
			if err == storage.ErrObjectNotExist || os.IsNotExist(err) {
				continue
			}
			log.Print(err)
			http.Error(w, "Internal Server Error", 500)
			return
		}
		for _, r := range resubmitFile.Resubmitted {
			body.ResubmitLookup[r.ToFile] = &Legislation{Legislation: db.Legislation{File: r.FromFile}}
		}
	}

	cacheTTL := time.Minute * 30

	err := a.getJSONFile(r.Context(), "build/last_sync.json", &body.LastSync)
	if err != nil {
		log.Print(err)
		http.Error(w, "Internal Server Error", 500)
		return
	}

	w.Header().Set("content-type", "text/html")
	a.addExpireHeaders(w, cacheTTL)
	err = t.ExecuteTemplate(w, "recent_legislation.html", body)
	if err != nil {
		log.Print(err)
		http.Error(w, "Internal Server Error", 500)
		return
	}
}
