package main

import (
	"context"
	"embed"
	_ "embed"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"cloud.google.com/go/storage"
	"github.com/gorilla/handlers"
	"github.com/jehiah/legislator/legistar"
	"github.com/julienschmidt/httprouter"
)

//go:embed templates/index.html
var content embed.FS

//go:embed static/*
var static embed.FS

type App struct {
	legistar *legistar.Client
	devMode  bool
	gsclient *storage.Client

	cachedRedirects map[string]string
	staticHandler   http.Handler
}

// Index returns the root path of `/`
func (a *App) Index(w http.ResponseWriter, r *http.Request, ps httprouter.Params) {
	var f fs.File
	var err error
	if a.devMode {
		f, err = os.Open("templates/index.html")
	} else {
		f, err = content.Open("templates/index.html")
	}

	if err != nil {
		log.Printf("%#v", err)
		http.Error(w, "error", 500)
		return
	}
	defer f.Close()
	w.Header().Set("content-type", "text/html")
	if !a.devMode {
		w.Header().Add("Cache-Control", "public; max-age=300")
	}
	io.Copy(w, f)
}

// IntroJSON proxies to /data/${year}.json to github:jehiah/nyc_legislation:introduction/$year/index.json
//
// Note: the router match pattern is `/:file/:year` so `:file` must be == "data"
func (a *App) IntroJSON(w http.ResponseWriter, r *http.Request, ps httprouter.Params) {
	path := ps.ByName("year")
	file := ps.ByName("file")
	if path == "local-law" && IsValidFileNumber(file) {
		a.LocalLaw(w, r, file)
		return
	}
	switch file {
	case "static":
		a.staticHandler.ServeHTTP(w, r)
		return
	case "data":
	default:
		log.Printf("file != data %q", file)
		http.Error(w, "Not Found", 404)
		return
	}

	if !strings.HasSuffix(path, ".json") {
		log.Printf("year %q", path)
		http.Error(w, "Not Found", 404)
		return
	}
	year, err := strconv.Atoi(strings.TrimSuffix(path, ".json"))
	if err != nil || year < 2014 || year > 2022 {
		log.Printf("year %d not found", year)
		http.Error(w, "Not Found", 404)
		return
	}

	rc, err := a.gsclient.Bucket("intronyc").Object(fmt.Sprintf("build/%d.json", year)).NewReader(r.Context())
	if err != nil {
		if err == storage.ErrObjectNotExist {
			if !a.devMode {
				w.Header().Add("Cache-Control", "public; max-age=300")
				w.Header().Add("Expires", time.Now().Add(time.Minute*5).Format(http.TimeFormat))
			}
			http.Error(w, "Not Found", 404)
			return
		}
		log.Printf("err %#v", err)
		http.Error(w, "error", 500)
		return
	}
	defer rc.Close()
	w.Header().Add("content-type", "application/json")
	if !a.devMode {
		w.Header().Add("Cache-Control", "public; max-age=300")
		w.Header().Add("Expires", time.Now().Add(time.Minute*5).Format(http.TimeFormat))
	}

	_, err = io.Copy(w, rc)
	if err != nil {
		log.Printf("%#v", err)
	}

}

// file == 1234-2020
func (a *App) LocalLaw(w http.ResponseWriter, r *http.Request, file string) {
	ctx := r.Context()
	if !IsValidFileNumber(file) {
		http.Error(w, "Not Found", 404)
		return
	}
	file = fmt.Sprintf("Int %s", file)

	filter := legistar.AndFilters(
		legistar.MatterTypeFilter("Introduction"),
		legistar.MatterFileFilter(file),
	)

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
	attachments, err := a.legistar.MatterAttachments(ctx, matters[0].ID)
	for _, attachment := range attachments {
		if strings.HasPrefix(attachment.Name, "Local Law") {
			if !a.devMode {
				w.Header().Add("Cache-Control", "public; max-age=604800")
				w.Header().Add("Expires", time.Now().Add(time.Hour*24*7).Format(http.TimeFormat))
			}
			http.Redirect(w, r, attachment.Link, 301)
			return
		}
	}
	http.Error(w, "Not Found", 404)
	return
}

// IsValidFileNumber matches 01234-2020
func IsValidFileNumber(file string) bool {
	if ok, _ := regexp.MatchString("^[0-9]{4}-20(14|15|16|17|18|19|20|21|22|23|24)$", file); !ok {
		return false
	}
	n := strings.Split(file, "-")
	seq, _ := strconv.Atoi(n[0])
	if seq > 3500 || seq < 1 {
		return false
	}
	year, _ := strconv.Atoi(n[1])
	if year > time.Now().Year() || year < 2014 {
		return false
	}
	return true
}

// FileRedirect redirects from /1234-2020 to the URL for File "Intro 1234-2020"
//
// Redirects are cached for the lifetime of the process but not persisted
func (a *App) FileRedirect(w http.ResponseWriter, r *http.Request, ps httprouter.Params) {

	file := ps.ByName("file")
	if !IsValidFileNumber(file) {
		http.Error(w, "Not Found", 404)
		return
	}
	file = fmt.Sprintf("Int %s", file)

	if redirect, ok := a.cachedRedirects[file]; ok {
		if !a.devMode {
			w.Header().Add("Cache-Control", "public; max-age=300")
		}
		http.Redirect(w, r, redirect, 302)
		return
	}

	filter := legistar.AndFilters(
		legistar.MatterTypeFilter("Introduction"),
		legistar.MatterFileFilter(file),
	)

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
	if !a.devMode {
		w.Header().Set("Cache-Control", "max-age=3600")
	}
	http.Redirect(w, r, redirect, 302)
}

func main() {
	logRequests := flag.Bool("log-requests", false, "log requests")
	devMode := flag.Bool("dev-mode", false, "development mode")
	flag.Parse()

	log.Print("starting server...")

	client, err := storage.NewClient(context.Background())
	if err != nil {
		log.Fatalf("Failed to create client: %v", err)
	}
	defer client.Close()

	app := &App{
		legistar:        legistar.NewClient("nyc", os.Getenv("NYC_LEGISLATOR_TOKEN")),
		gsclient:        client,
		devMode:         *devMode,
		cachedRedirects: make(map[string]string),
		staticHandler:   http.FileServer(http.FS(static)),
	}
	if *devMode {
		app.staticHandler = http.StripPrefix("/static/", http.FileServer(http.Dir("static")))
	}
	app.legistar.LookupURL, err = url.Parse("https://legistar.council.nyc.gov/gateway.aspx?m=l&id=")
	if err != nil {
		panic(err)
	}

	router := httprouter.New()
	router.GET("/", app.Index)
	router.GET("/:file/:year", app.IntroJSON)
	router.GET("/:file", app.FileRedirect)

	// Determine port for HTTP service.
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	var h http.Handler = router
	if *logRequests {
		h = handlers.LoggingHandler(os.Stdout, h)
	}

	// Start HTTP server.
	log.Printf("listening on port %s", port)
	if err := http.ListenAndServe(":"+port, h); err != nil {
		log.Fatal(err)
	}
}
