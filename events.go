package main

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"

	"cloud.google.com/go/storage"
	ics "github.com/arran4/golang-ical"
	"github.com/gosimple/slug"
	"github.com/jehiah/legislator/db"
)

type Event struct {
	db.Event
	Items []EventItem `json:",omitempty"`
}
type EventItem struct {
	db.EventItem
}

func (e EventItem) IsDraft() bool {
	if e.MatterFile == "" {
		return false
	}
	return strings.HasPrefix(e.MatterFile, "T")
}
func (e EventItem) Legislation() Legislation {
	return Legislation{
		Legislation: db.Legislation{
			File: e.MatterFile,
			Name: e.MatterName,
		},
	}
}

type EventPage struct {
	Page     string
	Title    string
	SubPage  string
	LastSync LastSync

	Session           Session
	IsCurrentSession  bool
	Committees        []string
	SelectedCommittee string
	CalendarFeed      string

	Events []Event
}

func (a *App) Events(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	templateName := "events.html"
	var err error

	t := newTemplate(a.templateFS, templateName)
	wantICS := strings.HasSuffix(r.URL.Path, ".ics")

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
	if wantICS {
		// show till start of previous month
		_, _, d := now.Date()
		now = now.AddDate(0, -1, -1*d)
	}
	for year := body.Session.StartYear; year <= body.Session.EndYear && year <= time.Now().Year(); year++ {

		var events []Event
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
			eventCommittee := slug.Make(TrimCommittee(e.BodyName))
			if eventCommittee != selectedCommittee && selectedCommittee != "" {
				continue
			}
			if selectedCommittee != "" && eventCommittee == selectedCommittee {
				body.SelectedCommittee = e.BodyName
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

	v := &url.Values{}
	if body.SelectedCommittee != "" {
		v.Set("committee", slug.Make(TrimCommittee(body.SelectedCommittee)))
	}
	body.CalendarFeed = (&url.URL{
		Scheme:   "https",
		Host:     "intro.nyc",
		Path:     "/events.ics",
		RawQuery: v.Encode(),
	}).String()

	if wantICS {
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

	if body.SelectedCommittee != "" {
		cal.SetName(body.SelectedCommittee)
		cal.SetDescription(fmt.Sprintf("NYC Council Calendar for %s", TrimCommittee(body.SelectedCommittee)))
	} else {
		cal.SetName("New York City Council Calendar")
	}
	cal.SetRefreshInterval("P1H") // 1 hour?
	v := &url.Values{}
	if body.SelectedCommittee != "" {
		v.Set("committee", slug.Make(TrimCommittee(body.SelectedCommittee)))
	}
	u := url.URL{
		Scheme:   "https",
		Host:     "intro.nyc",
		Path:     "/events.ics",
		RawQuery: v.Encode(),
	}
	cal.SetUrl(u.String())

	for _, e := range body.Events {
		if e.AgendaStatusName == "Deferred" {
			continue
		}
		event := cal.AddEvent(fmt.Sprintf("%d@intro.nyc", e.ID))
		event.SetCreatedTime(e.AgendaLastPublished)
		event.SetDtStampTime(e.AgendaLastPublished)
		event.SetModifiedAt(e.LastModified.Add(time.Second))
		event.SetStartAt(e.Date)
		event.SetEndAt(e.Date.Add(time.Hour))
		event.SetSummary(TrimCommittee(e.BodyName))
		if e.Location != "" {
			event.SetLocation(e.Location)
		}

		desc := &bytes.Buffer{}
		if e.AgendaStatusName != "Final" {
			fmt.Fprintf(desc, "Status: %s\n", e.AgendaStatusName)
		}
		for _, i := range e.Items {
			switch i.MatterType {
			case "Oversight":
				fmt.Fprintf(desc, "\n%s\n\n", i.Title)
			case "Introduction":
				if i.IsDraft() {
					fmt.Fprintf(desc, "%s ", i.MatterType)
				} else {
					fmt.Fprintf(desc, "https://intro.nyc/%s ", i.Legislation().IntroLink())
				}
				fmt.Fprintf(desc, "%s\n", i.MatterName)
			case "Resolution":
				if i.IsDraft() {
					fmt.Fprintf(desc, "%s ", i.MatterType)
				} else {
					fmt.Fprintf(desc, "https://intro.nyc/%s ", i.Legislation().IntroLink())
				}
				fmt.Fprintf(desc, "%s\n", i.MatterName)
			case "N/A":
				fmt.Fprintf(desc, "%s\n", i.MatterName)
			default:
				fmt.Fprintf(desc, "%s %s\n", i.MatterType, i.MatterName)
			}
		}
		if e.InSiteURL != "" {
			event.SetURL(e.InSiteURL)
			// TODO: event redirect?
			fmt.Fprintf(desc, "\n%s\n", e.InSiteURL)
		}
		event.SetDescription(strings.TrimSpace(desc.String()))
		// event.SetOrganizer("sender@domain", ics.WithCN("This Machine"))
		// event.AddAttendee("reciever or participant", ics.CalendarUserTypeIndividual, ics.ParticipationStatusNeedsAction, ics.ParticipationRoleReqParticipant, ics.WithRSVP(true))
	}

	if a.devMode {
		w.Header().Set("Content-type", "text/plain")
	} else {
		w.Header().Set("Content-type", "text/calendar")
	}
	io.WriteString(w, cal.Serialize())
}
