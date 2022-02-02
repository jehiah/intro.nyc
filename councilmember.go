package main

import (
	"fmt"
	"log"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"

	"cloud.google.com/go/storage"
	"github.com/jehiah/legislator/db"
	"github.com/julienschmidt/httprouter"
)

func (l Legislation) Statuses() []Status {
	d := make(map[string]int)
	for _, ll := range l.All {
		d[ll.StatusName] += 1
	}
	var o []Status
	for n, c := range d {
		o = append(o, Status{Name: n, Count: c, Percent: (float64(c) / float64(len(l.All))) * 100})
	}
	sortSeq := []string{
		// exhaustive from MatterStatuses
		"Adopted",
		"Approved",
		"Companion Pending Approval by Council",
		"Coupled on Call-Up Vote",
		"Defeated",
		"Deferred",
		"Disapproved",
		"Discharged from Committee",
		"Failed",
		"Filed",
		"General Orders Calendar",
		"Hearing Transcripts ",
		"Local Laws",
		"Press Conference Filed",
		"Press Conference Scheduled",
		"Received, Ordered, Printed and Filed",
		"Reported from Committee and Introduced",
		"Reported from Committee",
		"Returned Unsigned by Mayor",
		"Special Event Filed",
		"Special Event Scheduled",
		"Town Hall Meeting Filed",
		"Town Hall Meeting Scheduled",

		"Introduced",
		"Committee",
		"Laid Over in Committee",
		"Filed (End of Session)",

		"Vetoed",
		"Withdrawn",
		"Enacted (Charter Referendum)",
		"Enacted (Mayor's Desk for Signature)",
		"Enacted",
	}
	sortLookup := make(map[string]int, len(sortSeq))
	for i, s := range sortSeq {
		sortLookup[s] = i
	}

	sort.Slice(o, func(i, j int) bool { return sortLookup[o[i].Name] < sortLookup[o[j].Name] })

	// TODO: sort by sequence not by string
	return o
}

type Status struct {
	Name    string
	Count   int
	Percent float64
}

func (s Status) CSSClass() string {
	return "status-" + strings.ToLower(strings.Fields(s.Name)[0])
}

// Councilmember returns the list of councilmembers at /councilmembers/$name
func (a *App) Councilmember(w http.ResponseWriter, r *http.Request, ps httprouter.Params) {
	councilmember := ps.ByName("year")
	log.Printf("Councilmember %q", councilmember)
	if matched, err := regexp.MatchString("^[a-z-]+$", councilmember); err != nil {
		log.Print(err)
		http.Error(w, "Internal Server Error", 500)
		return
	} else if !matched {
		log.Printf("council member %q not found", councilmember)
		http.Error(w, "Not Found", 404)
		return
	}

	t := newTemplate(a.templateFS, "councilmember.html")

	var people []db.Person
	err := a.getJSONFile(r.Context(), "build/people_all.json", &people)
	if err != nil {
		if err == storage.ErrObjectNotExist {
			a.addExpireHeaders(w, time.Minute*5)
			http.Error(w, "Not Found", 404)
			return
		}
		log.Print(err)
		http.Error(w, "Internal Server Error", 500)
	}
	var person db.Person
	for _, p := range people {
		if p.Slug == councilmember {
			person = p
			break
		}
	}
	if person.Slug == "" {
		log.Printf("council member %q not found", councilmember)
		a.addExpireHeaders(w, time.Minute*5)
		http.Error(w, "Not Found", 404)
		return
	}

	cacheTTL := time.Minute * 15
	if person.End.Before(time.Now()) {
		cacheTTL = time.Hour
	}

	type Page struct {
		Page             string
		Person           Person
		LastSync         LastSync
		Legislation      Legislation
		PrimarySponsor   Legislation
		SecondarySponsor Legislation
	}
	body := Page{
		Page:   "councilmembers",
		Person: Person{Person: person},
	}

	// TODO: some files may be cached from previous sessions
	err = a.getJSONFile(r.Context(), fmt.Sprintf("build/legislation_%s.json", person.Slug), &body.Legislation.All)
	if err != nil {
		// not found is ok; it means they are likely not active in current session (yet?)
		if err != storage.ErrObjectNotExist {
			log.Print(err)
			http.Error(w, "Internal Server Error", 500)
		}
	}
	body.PrimarySponsor = body.Legislation.FilterPrimarySponsor(person.ID)
	body.SecondarySponsor = body.Legislation.FilterSecondarySponsor(person.ID)

	err = a.getJSONFile(r.Context(), "build/last_sync.json", &body.LastSync)
	if err != nil {
		log.Print(err)
		http.Error(w, "Internal Server Error", 500)
	}

	w.Header().Set("content-type", "text/html")
	a.addExpireHeaders(w, cacheTTL)
	err = t.ExecuteTemplate(w, "councilmember.html", body)
	if err != nil {
		log.Print(err)
		http.Error(w, "Internal Server Error", 500)
	}
}
