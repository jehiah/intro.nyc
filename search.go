package main

import (
	"log"
	"net/http"
	"time"
)

// Search returns the root path of `/` for in-browser search
func (a *App) Search(w http.ResponseWriter, r *http.Request) {
	T := Printer(r.Context())
	t := newTemplate(a.templateFS, "index.html")
	w.Header().Set("content-type", "text/html")
	a.addExpireHeaders(w, time.Minute*5)
	type Page struct {
		Page     string
		Title    string
		Sessions []Session
	}
	body := Page{
		Page:     "search",
		Title:    T.Sprintf("NYC Council Legislation Search"),
		Sessions: Sessions[1:5],
	}
	if time.Now().After(time.Date(2026, time.January, 15, 15, 0, 0, 0, time.UTC)) {
		body.Sessions = Sessions[:5]
	}
	err := t.ExecuteTemplate(w, "index.html", body)
	if err != nil {
		log.Print(err)
		http.Error(w, "Internal Server Error", 500)
		return
	}
}
