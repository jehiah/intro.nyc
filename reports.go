package main

import (
	"fmt"
	"log"
	"net/http"
	"sort"
	"time"

	"cloud.google.com/go/storage"
	"github.com/julienschmidt/httprouter"
)

// Reports handles /reports/...
func (a *App) Reports(w http.ResponseWriter, r *http.Request, ps httprouter.Params) {
	switch ps.ByName("report") {
	case "":
		http.Redirect(w, r, "/reports/most_sponsored", 302)
	case "most_sponsored":
		a.ReportMostSponsored(w, r)
	default:
		http.Error(w, "Not Found", 404)
	}
}

// ReportMostSponsored returns the list of legislation changes /recent
func (a *App) ReportMostSponsored(w http.ResponseWriter, r *http.Request) {
	template := "report_most_sponsored.html"

	t := newTemplate(a.templateFS, template)

	type Page struct {
		Page        string
		LastSync    LastSync
		Legislation LegislationList
	}
	body := Page{
		Page: "reports",
	}

	err := a.getJSONFile(r.Context(), "build/last_sync.json", &body.LastSync)
	if err != nil {
		log.Print(err)
		http.Error(w, "Internal Server Error", 500)
		return
	}

	// get all the years for the legislative session
	for year := CurrentSession.StartYear; year <= CurrentSession.EndYear && year <= time.Now().Year(); year++ {
		var l []Legislation
		err := a.getJSONFile(r.Context(), fmt.Sprintf("build/%d.json", year), &l)
		if err != nil {
			if err == storage.ErrObjectNotExist {
				continue
			}
			log.Print(err)
			http.Error(w, "Internal Server Error", 500)
			return
		}
		body.Legislation = append(body.Legislation, l...)
	}

	sort.Slice(body.Legislation, func(i, j int) bool { return len(body.Legislation[i].Sponsors) > len(body.Legislation[j].Sponsors) })

	w.Header().Set("content-type", "text/html")
	cacheTTL := time.Minute * 15
	a.addExpireHeaders(w, cacheTTL)
	err = t.ExecuteTemplate(w, template, body)
	if err != nil {
		log.Print(err)
		http.Error(w, "Internal Server Error", 500)
		return
	}
}
