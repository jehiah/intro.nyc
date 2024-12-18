package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/jehiah/legislator/db"
)

type Person struct {
	db.Person
	PersonMetadata
}

func (p Person) ID() int {
	return p.Person.ID
}
func (p Person) Borough() string {
	city := strings.TrimSpace(p.Person.DistrictOffice.City)
	switch city {
	case "Brooklyn", "Bronx", "Queens", "Staten Island", "Bronx and Manhattan":
		return city
	case "New York", "New York, NY 10033":
		return "Manhattan"
	case "Bayside", "Astoria", "Jackson Heights", "Far Rockaway", "Middle Village", "St. Albans", "Oakland Gardens", "Sunnyside", "Ozone Park", "Hillcrest":
		return "Queens"
	}

	// try based on district
	if strings.HasPrefix(p.WWW, "https://council.nyc.gov/district-") {
		district := strings.TrimSuffix(strings.TrimPrefix(p.WWW, "https://council.nyc.gov/district-"), "/")
		switch district {
		case "1", "2", "3", "4", "5", "6", "7", "9", "10":
			return "Manhattan"
		case "8":
			return "Manhattan and Bronx"
		case "11", "12", "13", "14", "15", "16", "17", "18":
			return "Bronx"
		case "19", "20", "21", "22", "23", "24", "25", "26", "27", "28", "29", "30", "31", "32":
			return "Queens"
		case "33", "35", "36", "37", "38", "39", "40", "41", "42", "43", "44", "45", "46", "47", "48":
			return "Brooklyn"
		case "34":
			return "Brooklyn and Queens"
		case "49", "50", "51":
			return "Staten Island"
		}
	}
	return ""
}

type PersonMetadata struct {
	ID                           int
	District                     int
	Slug                         string
	Twitter, TwitterPersonal     string
	Facebook, FacebookPersonal   string
	Instagram, InstagramPersonal string
	SocialAccounts               []SocialAccount
}

type SocialAccount struct {
	Username string
	Link     string
	Official bool
	Personal bool
	Platform string // twitter | instagram | facebook | threads
}

func (s SocialAccount) CSSClass() string {
	return s.Platform
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

func (p Person) CouncilTitle() string {
	now := time.Now()
	for _, oo := range p.OfficeRecords {
		if oo.BodyName != "City Council" {
			continue
		}
		if oo.End.Before(now) {
			continue
		}
		if oo.MemberType == "PRIMARY SPEAKER" {
			return "Speaker"
		}
		return oo.Title
	}
	return ""
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

// Party returns "(D)", "(R)"
func (p Person) Party() string {
	party := p.PartyShort()
	if party != "" {
		return fmt.Sprintf("(%s)", party)
	}
	return ""
}

// PartyShort returns "D", "R"
func (p Person) PartyShort() string {
	for _, oo := range p.OfficeRecords {
		switch oo.BodyName {
		case "Minority (Republican) Conference of the Council of the City of New York ":
			return "R"
		case "Democratic Conference of the Council of the City of New York ":
			return "D"
		}
	}
	return ""
}

func (a *App) GetCouncilMembers(ctx context.Context, session Session) ([]Person, error) {
	var people []db.Person
	err := a.getJSONFile(ctx, "build/people_active.json", &people)
	if err != nil {
		return nil, err
	}

	var metadata []PersonMetadata
	err = a.getJSONFile(ctx, "build/people_metadata.json", &metadata)
	if err != nil {
		return nil, err
	}
	metadataLookup := make(map[int]PersonMetadata)
	for _, m := range metadata {
		metadataLookup[m.ID] = m
	}
	var out []Person
	for _, p := range people {
		if !session.Overlaps(p.Start, p.End) {
			continue
		}
		out = append(out, Person{Person: p, PersonMetadata: metadataLookup[p.ID]})

	}
	return out, nil
}

// Councilmembers returns the list of councilmembers at /councilmembers
func (a *App) Councilmembers(w http.ResponseWriter, r *http.Request) {
	T := Printer(r.Context())
	t := newTemplate(a.templateFS, "councilmembers.html")

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
	var err error
	body.People, err = a.GetCouncilMembers(r.Context(), CurrentSession)
	if err != nil {
		log.Print(err)
		http.Error(w, "Internal Server Error", 500)
		return
	}
	err = a.getJSONFile(r.Context(), "build/last_sync.json", &body.LastSync)
	if err != nil {
		log.Print(err)
		http.Error(w, "Internal Server Error", 500)
		return
	}

	w.Header().Set("content-type", "text/html")
	cacheTTL := time.Minute * 30
	a.addExpireHeaders(w, cacheTTL)
	err = t.ExecuteTemplate(w, "councilmembers.html", body)
	if err != nil {
		log.Print(err)
		http.Error(w, "Internal Server Error", 500)
		return
	}
}
