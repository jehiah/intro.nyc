package main

import (
	"fmt"
	"html/template"
	"log"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"cloud.google.com/go/storage"
	"github.com/gosimple/slug"
	"github.com/jehiah/legislator/db"
	"github.com/julienschmidt/httprouter"
	// "gonum.org/v1/gonum/mat"
)

func TrimCommittee(s string) string {
	return strings.TrimPrefix(s, "Committee on ")
}

// Reports handles /reports/...
func (a *App) Reports(w http.ResponseWriter, r *http.Request, ps httprouter.Params) {
	switch ps.ByName("report") {
	case "":
		http.Redirect(w, r, "/reports/most_sponsored", 302)
	case "most_sponsored":
		a.ReportMostSponsored(w, r)
	case "status":
		a.ReportByStatus(w, r)
	case "similarity":
		a.ReportSimilarity(w, r)
	case "councilmembers":
		a.ReportCouncilmembers(w, r)
	case "committees":
		a.ReportCommittees(w, r)
	default:
		http.Error(w, "Not Found", 404)
	}
}

type CommitteeSponsorship struct {
	BodyName              string
	Sponsors              int
	CouncilmemberSponsors int
	CommitteeSponsors     int
	CommitteeMembers      int
}

func (c CommitteeSponsorship) Majority() bool {
	return c.CouncilmemberSponsors >= 26
}
func (c CommitteeSponsorship) SuperMajority() bool {
	return c.CouncilmemberSponsors >= 34
}
func (c CommitteeSponsorship) CommitteeMajority() bool {
	return c.CommitteeSponsors > (c.CommitteeMembers / 2)
}
func (c CommitteeSponsorship) CommitteeString() string {
	return fmt.Sprintf("%d of %d", c.CommitteeSponsors, c.CommitteeMembers)
}

// ReportMostSponsored returns the list of legislation changes /recent
func (a *App) ReportMostSponsored(w http.ResponseWriter, r *http.Request) {
	templateName := "report_most_sponsored.html"

	type Page struct {
		Page        string
		LastSync    LastSync
		Legislation LegislationList
		Committees  []string
		// Sessions    []Session
	}
	body := Page{
		Page: "reports",
		// Sessions: Sessions[:3],

	}

	err := a.getJSONFile(r.Context(), "build/last_sync.json", &body.LastSync)
	if err != nil {
		log.Print(err)
		http.Error(w, "Internal Server Error", 500)
		return
	}

	committeeMembers := make(map[string]map[int]bool)
	var people []db.Person
	err = a.getJSONFile(r.Context(), "build/people_all.json", &people)
	if err != nil {
		log.Print(err)
		http.Error(w, "Internal Server Error", 500)
	}
	now := time.Now()
	startDate := CurrentSession.StartDate()
	for _, p := range people {
		for _, or := range p.OfficeRecords {
			if !strings.HasPrefix(or.BodyName, "Committee") {
				continue
			}
			if or.Start.Before(startDate) || or.End.Before(now) {
				continue
			}
			if _, ok := committeeMembers[or.BodyName]; !ok {
				committeeMembers[or.BodyName] = make(map[int]bool)
			}
			committeeMembers[or.BodyName][p.ID] = true

			// some committees are "committe on a, b"
			if strings.Contains(or.BodyName, ",") {
				shortName := strings.Split(or.BodyName, ",")[0]
				if _, ok := committeeMembers[shortName]; !ok {
					committeeMembers[shortName] = make(map[int]bool)
				}
				committeeMembers[shortName][p.ID] = true
			}
		}
	}

	t := newTemplate(a.templateFS, templateName, template.FuncMap{
		"CommitteeSponsors": func(l Legislation) CommitteeSponsorship {
			m := committeeMembers[l.BodyName]
			c := CommitteeSponsorship{
				BodyName:         l.BodyName,
				CommitteeMembers: len(m),
				Sponsors:         len(l.Sponsors),
			}
			for _, s := range l.Sponsors {
				if s.ID == 0 {
					continue // i.e. BP, PA
				}
				c.CouncilmemberSponsors++
				if m[s.ID] {
					c.CommitteeSponsors++
				}
			}
			return c
		},
	})

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

	c := make(map[string]bool)
	for _, l := range body.Legislation {
		if l.BodyName == "Withdrawn" {
			continue
		}
		c[l.BodyName] = true
	}
	for b, _ := range c {
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

// ReportByStatus shows the change in bill status over time
func (a *App) ReportByStatus(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	template := "report_by_status.html"

	t := newTemplate(a.templateFS, template)

	type Row struct {
		Date   string
		Count  int
		Status string
		Time   time.Time `json:"-"`
		Last   bool      `json:"Last,omitempty"`
	}
	type Page struct {
		Page     string
		SubPage  string
		LastSync LastSync
		Data     []Row
		Session  Session
		Sessions []Session
	}
	body := Page{
		Page:     "reports",
		SubPage:  "by_status",
		Session:  CurrentSession,
		Sessions: Sessions,
	}
	for _, s := range Sessions {
		if s.String() == r.Form.Get("session") {
			body.Session = s
		}
	}

	introduced, hearing, approved, enacted := make(map[time.Time]int), make(map[time.Time]int), make(map[time.Time]int), make(map[time.Time]int)

	err := a.getJSONFile(r.Context(), "build/last_sync.json", &body.LastSync)
	if err != nil {
		log.Print(err)
		http.Error(w, "Internal Server Error", 500)
		return
	}

	// get all the years for the legislative session
	for year := body.Session.StartYear; year <= body.Session.EndYear && year <= time.Now().Year(); year++ {
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
		for _, ll := range l {
			if ll.StatusName == "Withdrawn" {
				continue
			}
			day := ll.IntroDate.In(americaNewYork).Truncate(time.Hour * 24)
			introduced[day] = introduced[day] + 1

			seen := make(map[string]bool)
			for _, h := range ll.History {
				if seen[h.Action] {
					continue
				}
				seen[h.Action] = true // only track first hearing
				day := h.Date.In(americaNewYork).Truncate(time.Hour * 24)

				switch h.Action {
				// use IntroDate directly; some bills don't have a matching action 0407-2022
				// case "Introduced by Council":
				// 	introduced[day] = introduced[day] + 1
				case "Hearing Held by Committee", "Hearing on P-C Item by Comm":
					hearing[day] = hearing[day] + 1
				case "Approved by Council":
					approved[day] = approved[day] + 1
				case "City Charter Rule Adopted", "Signed Into Law by Mayor",
					"Overridden by Council": // possible after "Vetoed by Mayor" (See Int 1208-2013)
					enacted[day] = enacted[day] + 1
				default:
					continue
				}
			}
		}
	}

	today := time.Now().In(americaNewYork).Truncate(time.Hour * 24)
	for i, d := range []map[time.Time]int{introduced, hearing, approved, enacted} {
		status := []string{"Introduced", "Hearing Held", "Passed Council", "Enacted"}[i]
		var data []Row
		for date, count := range d {
			data = append(data, Row{Time: date, Date: date.Format(time.RFC3339), Count: count, Status: status})
		}
		sort.Slice(data, func(i, j int) bool { return data[i].Time.Before(data[j].Time) })
		carry := 0
		for i, v := range data {
			data[i].Count += carry
			carry += v.Count
		}

		if body.Session == CurrentSession {
			// add current values as 'tomorrow'. This ensures todays values have a step to tomorrow
			last := data[len(data)-1]
			// show tomorrow
			tomorrow := today.AddDate(0, 0, 1)
			data = append(data, Row{Time: tomorrow, Date: tomorrow.Format(time.RFC3339), Count: last.Count, Status: last.Status})
		}
		data[len(data)-1].Last = true

		body.Data = append(body.Data, data...)

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

// ReportSimilarity shows how similar CMs are
func (a *App) ReportSimilarity(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	template := "report_similarity.html"

	t := newTemplate(a.templateFS, template)

	type Row struct {
		db.Person

		ExpectedSponsors int
		Sponsors         int
		SponsorPercent   float64
		ExpectedVotes    int
		Votes            int
		VotePercent      float64
	}
	data := make(map[int]*Row)

	type Page struct {
		Page     string
		SubPage  string
		LastSync LastSync
		Data     []Row
		Session  Session
		Sessions []Session
		People   []db.Person
		Person   db.Person
		Matrix   map[int]*Row
		Names    []string
	}
	body := Page{
		Page:     "reports",
		SubPage:  "by_status",
		Session:  CurrentSession,
		Sessions: Sessions,
		Matrix:   data,
	}
	for _, s := range Sessions {
		if s.String() == r.Form.Get("session") {
			body.Session = s
		}
	}

	var people []db.Person
	err := a.getJSONFile(r.Context(), "build/people_all.json", &people)
	if err != nil {
		log.Print(err)
		http.Error(w, "Internal Server Error", 500)
	}
	for _, p := range people {
		include := false
		for _, or := range p.OfficeRecords {
			if !body.Session.Overlaps(or.Start, or.End) {
				continue
			}
			include = true
			break
		}
		if include {
			body.People = append(body.People, p)
			data[p.ID] = &Row{Person: p}
		}
	}

	for _, p := range body.People {
		if p.Slug == r.Form.Get("councilmember") {
			body.Person = p
			break
		}
	}

	if body.Person.Slug == "" {
		if r.Form.Get("councilmember") != "" {
			http.Error(w, "Not Found", 404)
			return
		}
		params := &url.Values{
			"councilmember": []string{body.People[0].Slug},
			"session":       []string{body.Session.String()},
		}
		http.Redirect(w, r, "/reports/similarity?"+params.Encode(), 302)
		return
	}

	// get all the years for the legislative session
	for year := body.Session.StartYear; year <= body.Session.EndYear && year <= time.Now().Year(); year++ {
		var l []Legislation
		err := a.getJSONFile(r.Context(), fmt.Sprintf("build/%d_votes.json", year), &l)
		if err != nil {
			if err == storage.ErrObjectNotExist {
				continue
			}
			log.Print(err)
			http.Error(w, "Internal Server Error", 500)
			return
		}
		for _, ll := range l {
			// count sponsorship
			countSponsorship := false
			for _, s := range ll.Sponsors {
				if s.ID == body.Person.ID {
					countSponsorship = true
					break
				}
			}
			if countSponsorship {
				for _, s := range ll.Sponsors {
					r, ok := data[s.ID]
					if !ok {
						continue // skip sponsorships by BP's
					}
					r.Sponsors++
				}
			}

			// walk votes backwards; use the first one
			for i := len(ll.History) - 1; i >= 0; i-- {
				h := ll.History[i]
				// find desired vote
				var desired = 0

				for _, v := range h.Votes {
					if v.ID == body.Person.ID {
						desired = v.VoteID
					}
				}
				switch desired {
				case 12, 15:
					// Negative, Affirmative
				default:
					continue
				}

				// score everyone
				for _, v := range h.Votes {
					switch v.VoteID {
					case 11:
						// Abstain
					case 16:
						// Absent
						continue
					case 22, 44, 45, 46, 65, 43, 23, 9, 4:
						// Maternity, Paternity, Jury Duty, Medical, Bereavement, Conflict, Suspended, 	Non-voting, Excused
						continue
					}
					data[v.ID].ExpectedVotes++
					if v.VoteID == desired {
						data[v.ID].Votes++
					}
				}
			}
		}
	}
	expectedSponsors := data[body.Person.ID].Sponsors
	for _, r := range data {
		r.ExpectedSponsors = expectedSponsors
		if r.ExpectedSponsors > 0 {
			r.SponsorPercent = (float64(r.Sponsors) / float64(r.ExpectedSponsors)) * 100
		}
		if r.ExpectedVotes > 0 {
			r.VotePercent = (float64(r.Votes) / float64(r.ExpectedVotes)) * 100
		}
	}

	err = a.getJSONFile(r.Context(), "build/last_sync.json", &body.LastSync)
	if err != nil {
		log.Print(err)
		http.Error(w, "Internal Server Error", 500)
		return
	}

	w.Header().Set("content-type", "text/html")
	cacheTTL := time.Minute * 5
	// cacheTTL := time.Second
	a.addExpireHeaders(w, cacheTTL)
	err = t.ExecuteTemplate(w, template, body)
	if err != nil {
		log.Print(err)
		http.Error(w, "Internal Server Error", 500)
		return
	}

}

// ReportCouncilmembers shows the legislative activity of each councilmember
func (a *App) ReportCouncilmembers(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	template := "report_by_councilmember.html"

	t := newTemplate(a.templateFS, template)

	type Row struct {
		Person       db.PersonReference
		OfficeRecord db.OfficeRecord

		IntroIntro   int
		IntroHearing int
		IntroPassed  int
		IntroEnacted int
		IntroVeto    int

		SponsorIntro   int
		SponsorHearing int
		SponsorPassed  int
		SponsorEnacted int
		SponsorVeto    int
	}
	type Page struct {
		Page             string
		SubPage          string
		LastSync         LastSync
		Data             []Row
		Session          Session
		Sessions         []Session
		Committees       []string
		IsCurrentSession bool
	}
	body := Page{
		Page:             "reports",
		SubPage:          "by_councilmember",
		Session:          CurrentSession,
		Sessions:         Sessions,
		IsCurrentSession: true,
	}
	for _, s := range Sessions {
		if s.String() == r.Form.Get("session") {
			body.Session = s
			if s != CurrentSession {
				body.IsCurrentSession = false
			}
		}
	}
	selectedCommittee := r.Form.Get("committee")

	peopleOfficeRecord := make(map[string]db.OfficeRecord)
	if selectedCommittee != "" {
		var people []db.Person
		err := a.getJSONFile(r.Context(), "build/people_all.json", &people)
		if err != nil {
			log.Print(err)
			http.Error(w, "Internal Server Error", 500)
		}
		for _, p := range people {
			for _, or := range p.OfficeRecords {
				if !body.Session.Overlaps(or.Start, or.End) {
					continue
				}
				shortCommittee := slug.Make(TrimCommittee(or.BodyName))
				if shortCommittee == selectedCommittee {
					peopleOfficeRecord[p.Slug] = or
				}
				continue
			}
		}
	}

	err := a.getJSONFile(r.Context(), "build/last_sync.json", &body.LastSync)
	if err != nil {
		log.Print(err)
		http.Error(w, "Internal Server Error", 500)
		return
	}

	data := make(map[string]*Row)
	c := make(map[string]bool)

	// get all the years for the legislative session
	for year := body.Session.StartYear; year <= body.Session.EndYear && year <= time.Now().Year(); year++ {
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
		for _, ll := range l {
			if ll.StatusName == "Withdrawn" {
				continue
			}
			c[ll.BodyName] = true

			if selectedCommittee != "" {
				shortCommittee := slug.Make(strings.TrimPrefix(ll.BodyName, "Committee on "))
				if shortCommittee != selectedCommittee {
					continue
				}
			}

			var hasHearing, hasPassed, hasEnacted bool

			seen := make(map[string]bool)
			for _, h := range ll.History {
				if seen[h.Action] {
					continue
				}
				seen[h.Action] = true // only track first hearing
				switch h.Action {
				case "Hearing Held by Committee", "Hearing on P-C Item by Comm":
					hasHearing = true
				case "Approved by Council":
					hasPassed = true
				case "City Charter Rule Adopted", "Signed Into Law by Mayor",
					"Overridden by Council": // possible after "Vetoed by Mayor" (See Int 1208-2013)
					hasEnacted = true
				default:
					continue
				}
			}
			for i, s := range ll.Sponsors {
				r, ok := data[s.Slug]
				if !ok {
					r = &Row{Person: s, OfficeRecord: peopleOfficeRecord[s.Slug]}
					data[s.Slug] = r
				}
				if i == 0 {
					r.IntroIntro += 1
					if hasHearing {
						r.IntroHearing += 1
					}
					if hasPassed {
						r.IntroPassed += 1
					}
					if hasEnacted {
						r.IntroEnacted += 1
					}
					// TODO veto?
				} else {
					r.SponsorIntro += 1
					if hasHearing {
						r.SponsorHearing += 1
					}
					if hasPassed {
						r.SponsorPassed += 1
					}
					if hasEnacted {
						r.SponsorEnacted += 1
					}
				}
			}
		}
	}

	for _, r := range data {
		body.Data = append(body.Data, *r)
	}
	sort.Slice(body.Data, func(i, j int) bool {
		if body.Data[i].IntroIntro == body.Data[j].IntroIntro {
			return strings.Compare(body.Data[i].Person.FullName, body.Data[j].Person.FullName) == -1
		}

		return body.Data[i].IntroIntro > body.Data[j].IntroIntro
	})
	for b, _ := range c {
		body.Committees = append(body.Committees, strings.TrimPrefix(b, "Committee on "))
	}
	sort.Strings(body.Committees)

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

// ReportCommittees shows the legislative activity of each committee
func (a *App) ReportCommittees(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	template := "report_by_committee.html"

	t := newTemplate(a.templateFS, template)

	type Row struct {
		Committee         string
		BillTotal         int
		BillHearing       int
		BillCommitteeVote int
		BillPassedCouncil int
		BillEnacted       int

		HearingDates      map[string]bool
		OversightHearings int
	}

	type Page struct {
		Page     string
		SubPage  string
		LastSync LastSync
		Data     []Row
		Session  Session
		Sessions []Session
		// Committees       []string ?
		IsCurrentSession bool
	}
	body := Page{
		Page:             "reports",
		SubPage:          "by_committee",
		Session:          CurrentSession,
		Sessions:         Sessions,
		IsCurrentSession: true,
	}
	for _, s := range Sessions {
		if s.String() == r.Form.Get("session") {
			body.Session = s
			if s != CurrentSession {
				body.IsCurrentSession = false
			}
		}
	}
	// selectedCommittee := r.Form.Get("committee")

	err := a.getJSONFile(r.Context(), "build/last_sync.json", &body.LastSync)
	if err != nil {
		log.Print(err)
		http.Error(w, "Internal Server Error", 500)
		return
	}

	data := make(map[string]*Row)
	c := make(map[string]bool)

	// get all the years for the legislative session
	for year := body.Session.StartYear; year <= body.Session.EndYear && year <= time.Now().Year(); year++ {
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
		for _, ll := range l {
			if ll.StatusName == "Withdrawn" {
				continue
			}
			c[ll.BodyName] = true
			d := data[ll.BodyName]
			if d == nil {
				d = &Row{
					Committee:    TrimCommittee(ll.BodyName),
					HearingDates: make(map[string]bool),
				}
				data[ll.BodyName] = d
			}
			d.BillTotal += 1

			seen := make(map[string]bool)
			for _, h := range ll.History {
				switch h.BodyName {
				case "City Council", "Administration", ll.BodyName:
					// only count actions by primary committee
				default:
					continue
				}
				if seen[h.Action] {
					continue
				}
				seen[h.Action] = true // only track first hearing per committee
				switch h.Action {
				case "Referred to Comm by Council":
				case "Laid Over by Committee", "Amendment Proposed by Comm", "Amended by Committee":
					// committees[h.BodyName] = true
				case "Hearing Held by Committee", "Hearing on P-C Item by Comm":
					// committees[h.BodyName] = true
					// TODO: track all hearings on hearing dates
					d.HearingDates[h.Date.Format("2005-01-02")] = true
					d.BillHearing += 1
				case "Approved by Committee":
					d.BillCommitteeVote += 1
				case "Approved by Council":
					d.BillPassedCouncil += 1
				case "City Charter Rule Adopted", "Signed Into Law by Mayor",
					"Overridden by Council": // possible after "Vetoed by Mayor" (See Int 1208-2013)
					d.BillEnacted += 1
				}
			}

		}
	}

	for _, r := range data {
		body.Data = append(body.Data, *r)
	}
	sort.Slice(body.Data, func(i, j int) bool {
		return strings.Compare(body.Data[i].Committee, body.Data[j].Committee) == -1
	})

	// for b, _ := range c {
	// 	body.Committees = append(body.Committees, strings.TrimPrefix(b, "Committee on "))
	// }
	// sort.Strings(body.Committees)

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
