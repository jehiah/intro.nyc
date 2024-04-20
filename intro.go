package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jehiah/legislator/db"
	"github.com/jehiah/legislator/legistar"
)

// IsValidFileNumber matches 0123-2020
func IsValidFileNumber(file string) bool {
	if ok, _ := regexp.MatchString("^[0-9]{4}-(19|20)[9012][0-9]$", file); !ok {
		return false
	}
	n := strings.Split(file, "-")
	seq, _ := strconv.Atoi(n[0])
	if seq > 3500 || seq < 1 {
		return false
	}
	year, _ := strconv.Atoi(n[1])
	if year > time.Now().Year() || year < 1996 {
		return false
	}
	return true
}

func (a *App) FileRedirect(w http.ResponseWriter, r *http.Request) {
	file := r.PathValue("file")
	if IsValidFileNumber(file) {
		a.IntroRedirect(w, r, file)
		return
	}
	if strings.HasSuffix(file, ".json") && IsValidFileNumber(strings.TrimSuffix(file, ".json")) {
		a.IntroJSON(w, r, file)
		return
	}
	http.Error(w, "Not Found", 404)
}

// IntroRedirect redirects from /1234-2020 to the URL for File "Intro 1234-2020"
//
// Redirects are cached for the lifetime of the process but not persisted
func (a *App) IntroRedirect(w http.ResponseWriter, r *http.Request, file string) {
	if !IsValidFileNumber(file) {
		http.Error(w, "Not Found", 404)
		return
	}
	file = fmt.Sprintf("Int %s", file)

	if redirect, ok := a.cachedRedirects[file]; ok {
		a.addExpireHeaders(w, time.Hour)
		http.Redirect(w, r, redirect, 302)
		return
	}

	filter := legistar.AndFilters(
		legistar.MatterTypeFilter("Introduction"),
		legistar.MatterFileFilter(file),
	)

	// TODO: retry with a suffix -A for older years
	// i.e. Int 0804-1996-A

	matters, err := a.legistar.Matters(r.Context(), filter)
	if err != nil {
		log.Print(err)
		http.Error(w, "unknown error", 500)
		return
	}
	if len(matters) != 1 {
		// TODO: cache?
		http.Error(w, "Not Found", 404)
		return
	}

	// we have one
	redirect, err := a.legistar.LookupWebURL(r.Context(), matters[0].ID)
	if err != nil {
		log.Print(err)
		http.Error(w, "unknown error", 500)
		return
	}
	a.cachedRedirects[file] = redirect
	a.addExpireHeaders(w, time.Hour)
	http.Redirect(w, r, redirect, 302)
}

// IntroJSON returns a json to the URL for File "Intro 1234-2020"
func (a *App) IntroJSON(w http.ResponseWriter, r *http.Request, file string) {
	file = fmt.Sprintf("Int %s", strings.TrimSuffix(file, ".json"))
	ctx := r.Context()

	filter := legistar.AndFilters(
		legistar.MatterTypeFilter("Introduction"),
		legistar.MatterFileFilter(file),
	)

	// TODO: retry with a suffix -A for older years
	// i.e. Int 0804-1996-A

	matters, err := a.legistar.Matters(ctx, filter)
	if err != nil {
		log.Print(err)
		http.Error(w, "unknown error", 500)
		return
	}
	if len(matters) != 1 {
		// TODO: cache?
		http.Error(w, "Not Found", 404)
		return
	}

	l := db.NewLegislation(matters[0])
	sponsors, err := a.legistar.MatterSponsors(ctx, l.ID)
	if err != nil {
		log.Print(err)
		http.Error(w, "unknown error", 500)
		return
	}
	l.Sponsors = []db.PersonReference{}
	for _, p := range sponsors {
		if p.MatterVersion != l.Version {
			continue
		}
		s := db.NewPersonReference(p)
		s.FullName = strings.TrimSpace(s.FullName)
		l.Sponsors = append(l.Sponsors, s)
	}

	history, err := a.legistar.MatterHistories(ctx, l.ID)
	if err != nil {
		log.Print(err)
		http.Error(w, "unknown error", 500)
		return
	}
	l.History = nil
	for _, mh := range history {
		hh := db.NewHistory(mh)
		if hh.PassedFlagName != "" {
			votes, _ := a.legistar.EventVotes(ctx, hh.ID)
			hh.Votes = db.NewVotes(votes)
		}
		l.History = append(l.History, hh)
	}

	attachments, err := a.legistar.MatterAttachments(ctx, l.ID)
	if err != nil {
		log.Print(err)
		http.Error(w, "unknown error", 500)
		return
	}
	l.Attachments = nil
	for _, a := range attachments {
		l.Attachments = append(l.Attachments, db.NewAttachment(a))
	}

	versions, err := a.legistar.MatterTextVersions(ctx, l.ID)
	if err != nil {
		log.Print(err)
		http.Error(w, "unknown error", 500)
		return
	}
	l.TextID = versions.LatestTextID()
	txt, err := a.legistar.MatterText(ctx, l.ID, l.TextID)
	if err != nil {
		log.Print(err)
		http.Error(w, "unknown error", 500)
		return
	}
	l.Text = txt.SimplifiedText()
	l.RTF = txt.SimplifiedRTF()

	ttl := time.Hour
	if l.IntroDate.Year() < CurrentSession.StartYear {
		ttl = time.Hour * 48
	}

	a.addExpireHeaders(w, ttl)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(l)
}
