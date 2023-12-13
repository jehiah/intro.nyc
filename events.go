package main

import (
	"fmt"
	"log"
	"net/http"
	"sort"
	"time"

	"cloud.google.com/go/storage"
	"github.com/gosimple/slug"
	"github.com/jehiah/legislator/db"
	"github.com/julienschmidt/httprouter"
)

func (a *App) Events(w http.ResponseWriter, r *http.Request, ps httprouter.Params) {
	r.ParseForm()
	templateName := "events.html"
	var err error

	t := newTemplate(a.templateFS, templateName)

	type Page struct {
		Page     string
		Title    string
		SubPage  string
		LastSync LastSync

		Session          Session
		IsCurrentSession bool
		Committees       []string

		Events []db.Event
	}

	body := Page{
		Page:             "events",
		Title:            "NYC Council Events",
		Session:          CurrentSession,
		IsCurrentSession: true,
	}

	selectedCommittee := r.Form.Get("committee")

	committees := make(map[string]bool)
	eventCount := make(map[string]int)
	var people []db.Person
	err = a.getJSONFile(r.Context(), "build/people_all.json", &people)
	if err != nil {
		log.Print(err)
		http.Error(w, "Internal Server Error", 500)
		return
	}
	for _, p := range people {
		for _, or := range p.OfficeRecords {
			if !body.Session.Overlaps(or.Start, or.End) {
				continue
			}
			committees[TrimCommittee(or.BodyName)] = true
		}
	}

	now := time.Now().Add(time.Hour * 24 * 14 * -1)
	for year := body.Session.StartYear; year <= body.Session.EndYear && year <= time.Now().Year(); year++ {

		var events []db.Event
		err = a.getJSONFile(r.Context(), fmt.Sprintf("build/events_%d.json", year), &events)
		if err != nil {
			if err == storage.ErrObjectNotExist {
				continue
			}
			log.Print(err)
			http.Error(w, "Internal Server Error", 500)
			return
		}
		for _, e := range events {
			if e.Date.Before(now) {
				continue
			}
			eventCount[TrimCommittee(e.BodyName)]++
			if slug.Make(TrimCommittee(e.BodyName)) != selectedCommittee && selectedCommittee != "" {
				continue
			}
			body.Events = append(body.Events, e)
		}
	}

	for b, _ := range committees {
		if eventCount[b] == 0 {
			continue
		}
		body.Committees = append(body.Committees, TrimCommittee(b))
	}
	sort.Strings(body.Committees)

	w.Header().Set("content-type", "text/html")
	cacheTTL := time.Minute * 15
	a.addExpireHeaders(w, cacheTTL)
	err = t.ExecuteTemplate(w, templateName, body)
	if err != nil {
		log.Print(err)
		http.Error(w, "Internal Server Error", 500)
		return
	}

}
