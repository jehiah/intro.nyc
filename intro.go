package main

import (
	"fmt"
	"log"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jehiah/legislator/legistar"
	"github.com/julienschmidt/httprouter"
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

// IntroRedirect redirects from /1234-2020 to the URL for File "Intro 1234-2020"
//
// Redirects are cached for the lifetime of the process but not persisted
func (a *App) IntroRedirect(w http.ResponseWriter, r *http.Request, ps httprouter.Params) {
	file := ps.ByName("file")
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
