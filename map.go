package main

import (
	"log"
	"net/http"
	"time"
)

func (a *App) Map(w http.ResponseWriter, r *http.Request) {
	T := Printer(r.Context())
	templateName := "map.html"
	if r.URL.Query().Get("mode") == "iframe" {
		templateName = "map_iframe.html"
	}
	t := newTemplate(a.templateFS, templateName)
	w.Header().Set("content-type", "text/html")
	a.addExpireHeaders(w, time.Minute*5)
	type Page struct {
		Page  string
		Title string
	}
	body := Page{
		Page:  "map",
		Title: T.Sprintf("New York City Council District Map"),
	}
	err := t.ExecuteTemplate(w, templateName, body)
	if err != nil {
		log.Print(err)
		http.Error(w, "Internal Server Error", 500)
		return
	}
}
