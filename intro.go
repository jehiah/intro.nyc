package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/jehiah/legislator/db"
	"github.com/jehiah/legislator/legistar"
)

func (a *App) FileRedirect(w http.ResponseWriter, r *http.Request) {

	file := r.PathValue("file")
	if IsValidIntroID(file) {
		a.IntroRedirect(w, r, file)
		return
	}
	if strings.HasSuffix(file, ".json") && IsValidIntroID(strings.TrimSuffix(file, ".json")) {
		a.IntroJSON(w, r, file)
		return
	}
	if strings.HasSuffix(file, "+") && IsValidIntroID(strings.TrimSuffix(file, "+")) {
		a.IntroSummary(w, r)
		return
	}
	log.Printf("unknown file %q", file)
	http.Error(w, "Not Found", 404)
}

// IntroRedirect redirects from /1234-2020 to the URL for File "Intro 1234-2020"
//
// Redirects are cached for the lifetime of the process but not persisted
func (a *App) IntroRedirect(w http.ResponseWriter, r *http.Request, s string) {
	id, err := ParseIntroID(s)
	if err != nil {
		http.Error(w, "Not Found", 404)
		return
	}

	if redirect, ok := a.cachedRedirects[id]; ok {
		a.addExpireHeaders(w, time.Hour)
		http.Redirect(w, r, redirect, 302)
		return
	}

	filter := legistar.AndFilters(
		legistar.MatterTypeFilter(id.Type()),
		legistar.MatterFileFilter(id.File()),
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
	a.cachedRedirects[id] = redirect
	a.addExpireHeaders(w, time.Hour)
	http.Redirect(w, r, redirect, 302)
}

// IntroJSON returns a json to the URL for File "Intro 1234-2020"
func (a *App) IntroJSON(w http.ResponseWriter, r *http.Request, s string) {
	s = strings.TrimSuffix(s, ".json")
	id, err := ParseIntroID(s)
	if err != nil {
		http.Error(w, "Not Found", 404)
		return
	}

	ctx := r.Context()
	l, err := a.GetLegislation(ctx, id)
	if err != nil {
		log.Print(err)
		http.Error(w, "unknown error", 500)
		return
	}

	ttl := time.Hour
	if l.IntroDate.Year() < CurrentSession.StartYear {
		ttl = time.Hour * 48
	}

	a.addExpireHeaders(w, ttl)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(l)
}

func (a *App) GetLegislation(ctx context.Context, id IntroID) (*Legislation, error) {

	a.cacheMutex.RLock()
	if v, ok := a.cachedLegislation[id]; ok {
		if time.Since(v.Set) < time.Hour {
			a.cacheMutex.RUnlock()
			return v.Legislation, nil
		}
	}
	a.cacheMutex.RUnlock()

	filter := legistar.AndFilters(
		legistar.MatterTypeFilter(id.Type()),
		legistar.MatterFileFilter(id.File()),
	)

	// TODO: retry with a suffix -A for older years
	// i.e. Int 0804-1996-A
	matters, err := a.legistar.Matters(ctx, filter)
	if err != nil {
		return nil, err
	}
	if len(matters) != 1 {
		return nil, nil
	}

	l := db.NewLegislation(matters[0])
	sponsors, err := a.legistar.MatterSponsors(ctx, l.ID)
	if err != nil {
		return nil, err
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
		return nil, err
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
		return nil, err
	}
	l.Attachments = nil
	for _, a := range attachments {
		l.Attachments = append(l.Attachments, db.NewAttachment(a))
	}

	versions, err := a.legistar.MatterTextVersions(ctx, l.ID)
	if err != nil {
		return nil, err
	}
	l.TextID = versions.LatestTextID()
	txt, err := a.legistar.MatterText(ctx, l.ID, l.TextID)
	if err != nil {
		return nil, err
	}
	l.Text = txt.SimplifiedText()
	l.RTF = txt.SimplifiedRTF()

	v := Legislation{l}

	a.cacheMutex.Lock()
	a.cachedLegislation[id] = &CachedLegislation{
		Set:         time.Now(),
		Legislation: &v,
	}
	a.cacheMutex.Unlock()

	return &v, nil
}

func (a *App) IntroSummary(w http.ResponseWriter, r *http.Request) {
	s := strings.TrimSuffix(r.PathValue("file"), "+")
	id, err := ParseIntroID(s)
	if err != nil {
		http.Error(w, "Not Found", 404)
		return
	}
	ctx := r.Context()

	l, err := a.GetLegislation(ctx, id)
	if err != nil {
		log.Print(err)
		http.Error(w, "unknown error", 500)
		return
	}
	if l == nil {
		http.Error(w, "Not Found", 404)
		return
	}

	ttl := time.Hour
	if l.IntroDate.Year() < CurrentSession.StartYear {
		ttl = time.Hour * 48
	}
	a.addExpireHeaders(w, ttl)

	template := "bill_detail.html"
	t := newTemplate(a.templateFS, template)

	type Page struct {
		Page           string
		SubPage        string
		Legislation    Legislation
		Councilmembers []Person
		SponsorSlugs   []string
	}
	body := Page{
		Page:        "",
		SubPage:     "",
		Legislation: *l,
	}
	for _, s := range l.Sponsors {
		body.SponsorSlugs = append(body.SponsorSlugs, s.Slug)
	}
	body.Councilmembers, err = a.GetCouncilMembers(ctx, FindSession(l.IntroDate.Year()))
	if err != nil {
		log.Print(err)
		http.Error(w, "unknown error", 500)
		return
	}
	err = t.ExecuteTemplate(w, template, body)
	if err != nil {
		log.Print(err)
		http.Error(w, "unknown error", 500)
		return
	}
}
