package main

import (
	"log"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/jehiah/legislator/db"
	"github.com/julienschmidt/httprouter"
)

type Person struct {
	db.Person
	PersonMetadata
}

func (p Person) ID() int {
	return p.Person.ID
}
func (p Person) Borough() string {
	city := p.Person.DistrictOffice.City
	switch city {
	case "Brooklyn", "Bronx", "Queens", "Staten Island", "Bronx and Manhattan":
		return city
	case "New York", "New York, NY 10033":
		return "Manhattan"
	}
	return "Queens"
}

type PersonMetadata struct {
	ID                           int
	District                     int
	Twitter, TwitterPersonal     string
	Facebook, FacebookPersonal   string
	Instagram, InstagramPersonal string
}
type SocialAccount struct {
	Username string
	Link     string
	CSSClass string
}

func twitterUsername(s string) string {
	if s == "" {
		return ""
	}
	u, err := url.Parse(s)
	if err != nil {
		return ""
	}
	return "@" + strings.TrimPrefix(u.Path, "/")
}
func facebookUsername(s string) string {
	if s == "" {
		return ""
	}
	u, err := url.Parse(s)
	if err != nil {
		return ""
	}
	if strings.Contains(u.Path, "profile.php") {
		return "Facebook"
	}
	return strings.Trim(u.Path, "/")
}
func instagramUsername(s string) string {
	if s == "" {
		return ""
	}
	u, err := url.Parse(s)
	if err != nil {
		return ""
	}
	return strings.Trim(u.Path, "/")
}

func (t PersonMetadata) SocialAccounts() []SocialAccount {
	accounts := []SocialAccount{
		{twitterUsername(t.Twitter), t.Twitter, "twitter"},
		{twitterUsername(t.TwitterPersonal), t.TwitterPersonal, "twitter"},
		{facebookUsername(t.Facebook), t.Facebook, "facebook"},
		{facebookUsername(t.FacebookPersonal), t.FacebookPersonal, "facebook"},
		{instagramUsername(t.Instagram), t.Instagram, "instagram"},
		{instagramUsername(t.InstagramPersonal), t.InstagramPersonal, "instagram"},
	}
	var o []SocialAccount
	for _, a := range accounts {
		if a.Link != "" {
			o = append(o, a)
		}
	}
	return o
}

func (p Person) ActiveOfficeRecords() []db.OfficeRecord {
	var final []db.OfficeRecord
	now := time.Now()
	for _, oo := range p.OfficeRecords {
		if oo.End.Before(now) {
			continue
		}
		switch oo.BodyName {
		case "Committee of the Whole":
			continue
		case "City Council":
			continue
		case "Minority (Republican) Conference of the Council of the City of New York ":
			continue
		case "Democratic Conference of the Council of the City of New York ":
			continue
		}
		final = append(final, oo)
	}
	sort.Slice(final, func(i, j int) bool { return final[i].BodyName < final[j].BodyName })

	return final
}
func (p Person) Party() string {
	for _, oo := range p.OfficeRecords {
		switch oo.BodyName {
		case "Minority (Republican) Conference of the Council of the City of New York ":
			return "(R)"
		case "Democratic Conference of the Council of the City of New York ":
			return "(D)"
		}
	}
	return ""
}

// Councilmembers returns the list of councilmembers at /councilmembers
func (a *App) Councilmembers(w http.ResponseWriter, r *http.Request, ps httprouter.Params) {
	T := Printer(r.Context())
	t := newTemplate(a.templateFS, "councilmembers.html")

	var people []db.Person
	err := a.getJSONFile(r.Context(), "build/people_active.json", &people)
	if err != nil {
		log.Print(err)
		http.Error(w, "Internal Server Error", 500)
		return
	}

	cacheTTL := time.Minute * 30

	type Page struct {
		Page     string
		Title    string
		People   []Person
		LastSync LastSync
	}
	body := Page{
		Page:  "councilmembers",
		Title: T.Sprintf("NYC Council Members"),
	}
	var metadata []PersonMetadata
	err = a.getJSONFile(r.Context(), "build/people_metadata.json", &metadata)
	if err != nil {
		log.Print(err)
		http.Error(w, "Internal Server Error", 500)
		return
	}
	metadataLookup := make(map[int]PersonMetadata)
	for _, m := range metadata {
		metadataLookup[m.ID] = m
	}
	for _, p := range people {
		body.People = append(body.People, Person{Person: p, PersonMetadata: metadataLookup[p.ID]})
	}

	err = a.getJSONFile(r.Context(), "build/last_sync.json", &body.LastSync)
	if err != nil {
		log.Print(err)
		http.Error(w, "Internal Server Error", 500)
		return
	}

	w.Header().Set("content-type", "text/html")
	a.addExpireHeaders(w, cacheTTL)
	err = t.ExecuteTemplate(w, "councilmembers.html", body)
	if err != nil {
		log.Print(err)
		http.Error(w, "Internal Server Error", 500)
		return
	}
}
