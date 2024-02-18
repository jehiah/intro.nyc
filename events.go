package main

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"cloud.google.com/go/storage"
	ics "github.com/arran4/golang-ical"
	"github.com/gosimple/slug"
	"github.com/jehiah/legislator/db"
	"github.com/julienschmidt/httprouter"
)

type EventPage struct {
	Page     string
	Title    string
	SubPage  string
	LastSync LastSync

	Session          Session
	IsCurrentSession bool
	Committees       []string

	Events []db.Event
}

func (a *App) Events(w http.ResponseWriter, r *http.Request, ps httprouter.Params) {
	r.ParseForm()
	templateName := "events.html"
	var err error

	t := newTemplate(a.templateFS, templateName)

	body := EventPage{
		Page:             "events",
		Title:            "NYC Council Events",
		Session:          CurrentSession,
		IsCurrentSession: true,
	}

	err = a.getJSONFile(r.Context(), "build/last_sync.json", &body.LastSync)
	if err != nil {
		log.Print(err)
		http.Error(w, "Internal Server Error", 500)
		return
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

	now := time.Now().In(americaNewYork).Truncate(time.Hour * 24)
	for year := body.Session.StartYear; year <= body.Session.EndYear && year <= time.Now().Year(); year++ {

		var events []db.Event
		err = a.getJSONFile(r.Context(), fmt.Sprintf("build/events_%d.json", year), &events)
		if err != nil {
			if err == storage.ErrObjectNotExist || os.IsNotExist(err) {
				continue
			}
			log.Print(err)
			http.Error(w, "Internal Server Error", 500)
			return
		}
		for _, e := range events {
			eventCount[TrimCommittee(e.BodyName)]++
			if slug.Make(TrimCommittee(e.BodyName)) != selectedCommittee && selectedCommittee != "" {
				continue
			}
			if e.Date.Before(now) {
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

	if r.Form.Get("format") == "ics" {
		a.CalendarFile(w, body)
		return
	}

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

func (a *App) CalendarFile(w http.ResponseWriter, body EventPage) {
	cal := ics.NewCalendar()
	cal.SetMethod(ics.MethodPublish)
	// cal.AddTimezone("America/New_York")
	// timezone requires additional information
	for _, e := range body.Events {
		event := cal.AddEvent(fmt.Sprintf("%d@intro.nyc", e.ID))
		event.SetCreatedTime(e.AgendaLastPublished)
		event.SetDtStampTime(e.AgendaLastPublished)
		event.SetModifiedAt(e.LastModified)
		event.SetStartAt(e.Date)
		event.SetEndAt(e.Date.Add(time.Hour))
		event.SetSummary(e.BodyName)
		if e.Location != "" {
			event.SetLocation(e.Location)
		}
		// event.SetDescription("Description")
		desc := &bytes.Buffer{}
		if e.AgendaStatusName != "Final" {
			fmt.Fprintf(desc, "Status: %s\n", e.AgendaStatusName)
		}
		event.SetDescription(strings.TrimSpace(desc.String()))
		if e.InSiteURL != "" {
			event.SetURL(e.InSiteURL)
		}
		// event.SetOrganizer("sender@domain", ics.WithCN("This Machine"))
		// event.AddAttendee("reciever or participant", ics.CalendarUserTypeIndividual, ics.ParticipationStatusNeedsAction, ics.ParticipationRoleReqParticipant, ics.WithRSVP(true))
	}

	// w.Header().Set("Content-type", "text/plain") // TODO: text/calendar
	if a.devMode {
		w.Header().Set("Content-type", "text/plain")
	} else {
		w.Header().Set("Content-type", "text/calendar")
	}
	io.WriteString(w, cal.Serialize())
}
