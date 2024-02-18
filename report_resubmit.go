package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"cloud.google.com/go/storage"
	"github.com/jehiah/legislator/db"
)

// ReportResubmit shows bills to be re-submitted
func (a *App) ReportResubmit(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	template := "report_resubmit.html"

	t := newTemplate(a.templateFS, template)

	type Row struct {
		Legislation
		NewLegislation *Legislation
	}
	// data := make(map[int]*Row)

	type Page struct {
		Page     string
		SubPage  string
		LastSync LastSync

		Person db.Person
		People []db.Person

		Session         Session
		PreviousSession Session
		Sessions        []Session

		Data             []*Row
		IsCurrentSession bool
		ResubmittedOnly  bool

		BillCount   int
		Resubmitted int
	}
	body := Page{
		Page:    "reports",
		SubPage: "resubmit",

		Session:          CurrentSession,
		PreviousSession:  Sessions[1],
		IsCurrentSession: true,
		Sessions:         Sessions[:len(Sessions)-1], // skip the last one
		ResubmittedOnly:  r.Form.Get("resubmitted") == "only",
	}

	for i, s := range body.Sessions {
		if s.String() == r.Form.Get("session") {
			body.Session = s
			body.PreviousSession = Sessions[i+1]
			body.IsCurrentSession = i == 0
		}
	}

	// var metadata []PersonMetadata
	// err = a.getJSONFile(r.Context(), "build/people_metadata.json", &metadata)
	// if err != nil {
	// 	log.Print(err)
	// 	http.Error(w, "Internal Server Error", 500)
	// 	return
	// }
	// metadataLookup := make(map[int]PersonMetadata)
	// for _, m := range metadata {
	// 	metadataLookup[m.ID] = metadataLookup[]
	// }

	var people []db.Person
	err := a.getJSONFile(r.Context(), "build/people_all.json", &people)
	if err != nil {
		log.Print(err)
		http.Error(w, "Internal Server Error", 500)
		return
	}
	for _, p := range people {
		var current, previous bool
		for _, or := range p.OfficeRecords {
			switch {
			case or.BodyName != "City Council":
				continue
			case or.MemberType == "PRIMARY PUBLIC ADVOCATE":
				continue
			}
			if body.Session.Overlaps(or.Start, or.End) {
				current = true
			}
			if body.PreviousSession.Overlaps(or.Start, or.End) {
				previous = true
			}
		}
		if current && previous {
			body.People = append(body.People, p)
			if p.Slug == r.Form.Get("councilmember") {
				body.Person = p
			}
			// data[p.ID] = &Row{Person: Person{Person: p}}
		}
	}

	resubmitName := make(map[string]*Legislation, len(body.Data))
	resubmitTitle := make(map[string]*Legislation, len(body.Data))

	currentYear := time.Now().Year()
	for year := body.Session.StartYear; year <= body.Session.EndYear && year <= currentYear; year++ {
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
		for _, ll := range l {
			ll := ll
			switch ll.StatusName {
			case "Withdrawn":
				// "Enacted (Charter Referendum)",
				// "Enacted (Mayor's Desk for Signature)",
				// "Enacted",
				// "City Charter Rule Adopted":
				continue
			}
			resubmitName[ll.Title] = &ll
			resubmitTitle[ll.Title] = &ll
		}
	}

	// get all the years for the previous legislative session
	for year := body.PreviousSession.StartYear; year <= body.PreviousSession.EndYear; year++ {
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
		for _, ll := range l {
			switch ll.StatusName {
			case "Withdrawn",
				"Enacted (Charter Referendum)",
				"Enacted (Mayor's Desk for Signature)",
				"Enacted",
				"City Charter Rule Adopted":
				continue // no need for re-introduction
			}

			if body.Person.ID > 0 && !ll.SponsoredBy(body.Person.ID) {
				continue
			}

			new := resubmitName[ll.Name]
			if new == nil {
				new = resubmitTitle[ll.Title]
			}
			if new != nil {
				body.Resubmitted++
			}
			body.BillCount++
			if body.ResubmittedOnly && new == nil {
				continue
			}

			body.Data = append(body.Data, &Row{
				Legislation:    ll,
				NewLegislation: new,
			})
		}
	}

	err = a.getJSONFile(r.Context(), "build/last_sync.json", &body.LastSync)
	if err != nil {
		log.Print(err)
		http.Error(w, "Internal Server Error", 500)
		return
	}

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