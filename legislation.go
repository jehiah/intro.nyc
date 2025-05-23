package main

import (
	"fmt"
	"html/template"
	"sort"
	"time"

	"github.com/jehiah/legislator/db"
)

type LegislationList []Legislation

type Legislation struct {
	db.Legislation
}

// IntroID returns the file number without the "Int " or "Res " prefix
func (ll Legislation) IntroID() IntroID {
	i, _ := ParseFile(ll.File)
	return i
}

// FileNumber returns the File prefix as a number (without the session year)
func (ll Legislation) FileNumber() int {
	return ll.IntroID().FileNumber()
}

// FileYear returns the session year of the legislation
func (ll Legislation) FileYear() int {
	return ll.IntroID().FileYear()
}

func (ll Legislation) Session() Session {
	return FindSession(ll.FileYear())
}

func (ll Legislation) IntroLink() template.URL {
	return template.URL("/" + string(ll.IntroID()))
}
func (ll Legislation) IntroLinkText() string {
	return "intro.nyc" + string(ll.IntroLink())
}
func (ll Legislation) NumberSponsors() int {
	return len(ll.Sponsors)
}
func (ll Legislation) PrimarySponsor() db.PersonReference {
	return ll.Sponsors[0]
}
func (ll Legislation) SponsoredBy(id int) bool {
	for _, s := range ll.Sponsors {
		if s.ID == id {
			return true
		}
	}
	return false
}
func (ll Legislation) Hearings() []db.History {
	var o []db.History
	for _, h := range ll.History {
		switch h.Action {
		case "Hearing Held by Committee", "Hearing on P-C Item by Comm":
			o = append(o, h)
		}
	}
	return o
}
func (ll Legislation) Votes() []History {
	var o []History
	for _, h := range ll.History {
		switch h.Action {
		case "Approved by Committee", "Approved by Council":
			o = append(o, History{h})
		}
	}
	return o
}

type History struct {
	db.History
}

func (h History) VotePassed() bool {
	ayes, nayes, _ := h.getVotes()
	return ayes > nayes
}

func (h History) VoteSummary() string {
	ayes, nays, abstains := h.getVotes()
	return fmt.Sprintf("%d:%d:%d", ayes, abstains, nays)
}

func (h History) getVotes() (ayes int, nays int, abstains int) {
	for _, v := range h.Votes {
		switch v.Vote {
		case "Affirmative":
			ayes++
		case "Negative":
			nays++
		case "Abstain":
			abstains++
		}
	}
	return
}

func (ll Legislation) RecentAction() (string, time.Time) {
	// walk in reverse
	for i := len(ll.History) - 1; i >= 0; i-- {
		h := ll.History[i]
		switch h.Action {
		case "Introduced by Council",
			"Amended by Committee",
			"Approved by Committee",
			"Approved by Council",
			"Hearing Held by Committee",
			"Withdrawn",
			"Vetoed by Mayor",
			"City Charter Rule Adopted":
			return h.Action, h.Date
		}
	}
	return "", time.Unix(0, 0)
}
func (ll Legislation) RecentDate() time.Time {
	_, dt := ll.RecentAction()
	return dt
}
func (ll Legislation) IsRecent() bool {
	_, dt := ll.RecentAction()
	return time.Now().Add(time.Hour * 24 * -14).Before(dt)
}

func (l LegislationList) Number() int {
	return len(l)
}

func (l LegislationList) FilterPrimarySponsor(sponsor int) LegislationList {
	var o []Legislation
	for _, ll := range l {
		if len(ll.Sponsors) > 0 && ll.Sponsors[0].ID == sponsor {
			o = append(o, ll)
		}
	}
	return LegislationList(o)
}

func (l LegislationList) FilterSecondarySponsor(sponsor int) LegislationList {
	var o []Legislation
	for _, ll := range l {
		if len(ll.Sponsors) > 1 {
			for _, s := range ll.Sponsors[1:] {
				if s.ID == sponsor {
					o = append(o, ll)
				}
			}
		}
	}
	return LegislationList(o)
}

func (l LegislationList) Recent(d time.Duration) []RecentLegislation {
	cut := time.Now().In(americaNewYork).Add(-1 * d)
	var r []RecentLegislation
	for _, ll := range l {
		rr := NewRecentLegislation(ll)
		if rr.Date.Before(cut) {
			continue
		}
		r = append(r, rr)
	}
	sort.Slice(r, func(i, j int) bool { return r[i].Date.Before(r[j].Date) })
	return r
}
