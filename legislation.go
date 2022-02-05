package main

import (
	"html/template"
	"sort"
	"strings"
	"time"

	"github.com/jehiah/legislator/db"
)

type LegislationList []Legislation

type Legislation struct {
	db.Legislation
}

func (ll Legislation) IntroLink() template.URL {
	f := strings.TrimPrefix(ll.File, "Int ")
	// some older entries have "Int 0349-1998-A"
	if strings.Count(f, "-") == 2 {
		f = strings.Join(strings.Split(f, "-")[:2], "-")
	}
	return template.URL("/" + f)
}
func (ll Legislation) IntroLinkText() string {
	return "intro.nyc" + string(ll.IntroLink())
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
