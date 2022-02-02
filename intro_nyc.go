package main

import (
	"bytes"
	"context"
	"embed"
	_ "embed"
	"encoding/json"
	"flag"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"cloud.google.com/go/storage"
	"github.com/dustin/go-humanize"
	"github.com/gorilla/handlers"
	"github.com/jehiah/legislator/db"
	"github.com/jehiah/legislator/legistar"
	"github.com/julienschmidt/httprouter"
)

//go:embed templates/*
var content embed.FS

//go:embed static/*
var static embed.FS

var americaNewYork, _ = time.LoadLocation("America/New_York")

type App struct {
	legistar *legistar.Client
	devMode  bool
	gsclient *storage.Client

	cachedRedirects map[string]string
	staticHandler   http.Handler
	templateFS      fs.FS

	fileCache  map[string]CachedFile
	cacheMutex sync.RWMutex
}

type CachedFile struct {
	Body []byte
	Date time.Time
}

func commaInt(i int) string {
	return humanize.Comma(int64(i))
}

func newTemplate(fs fs.FS, n string) *template.Template {
	funcMap := template.FuncMap{
		"ToLower": strings.ToLower,
		"Comma":   commaInt,
		"Time":    humanize.Time,
	}
	t := template.New("empty").Funcs(funcMap)
	return template.Must(t.ParseFS(fs, filepath.Join("templates", n), "templates/base.html"))
}

// RobotsTXT renders /robots.txt
func (a *App) RobotsTXT(w http.ResponseWriter, r *http.Request, ps httprouter.Params) {
	w.Header().Set("content-type", "text/plain")
	a.addExpireHeaders(w, time.Hour*24*7)
	io.WriteString(w, "# robots welcome\n# https://github.com/jehiah/intro.nyc\n")
}

type LastSync struct {
	LastRun time.Time
}

type Legislation struct {
	All []db.Legislation
}

func (l Legislation) FilterPrimarySponsor(sponsor int) Legislation {
	var o []db.Legislation
	for _, ll := range l.All {
		if len(ll.Sponsors) > 0 && ll.Sponsors[0].ID == sponsor {
			o = append(o, ll)
		}
	}
	return Legislation{All: o}
}
func (l Legislation) FilterSecondarySponsor(sponsor int) Legislation {
	var o []db.Legislation
	for _, ll := range l.All {
		if len(ll.Sponsors) > 1 {
			for _, s := range ll.Sponsors[1:] {
				if s.ID == sponsor {
					o = append(o, ll)
				}
			}
		}
	}
	return Legislation{All: o}
}

func (l Legislation) Recent(d time.Duration) []RecentLegislation {
	cut := time.Now().In(americaNewYork).Add(-1 * d)
	var r []RecentLegislation
	for _, ll := range l.All {
		rr := NewRecentLegislation(ll)
		if rr.Date.Before(cut) {
			continue
		}
		r = append(r, rr)
	}
	sort.Slice(r, func(i, j int) bool { return r[i].Date.Before(r[j].Date) })
	return r
}

func (a *App) getJSONFile(ctx context.Context, filename string, v interface{}) error {
	f, err := a.getFile(ctx, filename)
	if err != nil {
		return err
	}
	return json.NewDecoder(f).Decode(v)
}

func (a *App) getFile(ctx context.Context, filename string) (io.Reader, error) {
	maxTTL := time.Minute * 5
	cut := time.Now().Add(-1 * maxTTL)
	a.cacheMutex.RLock()
	if c, ok := a.fileCache[filename]; ok && c.Date.After(cut) {
		a.cacheMutex.RUnlock()
		return bytes.NewBuffer(c.Body), nil
	}
	a.cacheMutex.RUnlock()
	log.Printf("get gs://intronyc/%s", filename)
	r, err := a.gsclient.Bucket("intronyc").Object(filename).NewReader(ctx)
	if err != nil {
		return nil, err
	}
	body, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	a.cacheMutex.Lock()
	defer a.cacheMutex.Unlock()
	a.fileCache[filename] = CachedFile{Body: body, Date: time.Now()}
	return bytes.NewBuffer(body), nil
}

func (a *App) addExpireHeaders(w http.ResponseWriter, duration time.Duration) {
	if a.devMode {
		return
	}
	w.Header().Add("Cache-Control", fmt.Sprintf("public; max-age=%d", int(duration.Seconds())))
	w.Header().Add("Expires", time.Now().Add(duration).Format(http.TimeFormat))
}

// L2 /:file/:year
func (a *App) L2(w http.ResponseWriter, r *http.Request, ps httprouter.Params) {
	path := ps.ByName("year")
	file := ps.ByName("file")
	if path == "local-law" && IsValidFileNumber(file) {
		a.LocalLaw(w, r, file)
		return
	}
	if file == "local-laws" {
		a.LocalLaws(w, r, ps)
		return
	}
	if file == "councilmembers" {
		a.Councilmember(w, r, ps)
		return
	}
	if file == "static" {
		a.staticHandler.ServeHTTP(w, r)
		return
	}
	if file == "data" {
		a.ProxyJSON(w, r, ps)
		return
	}
	http.Error(w, "Not Found", 404)
	return
}

// ProxyJSON proxies to /data/file.json to gs://intronyc/build/$file.json
//
// Note: the router match pattern is `/:file/:year` so `:file` must be == "data"
func (a *App) ProxyJSON(w http.ResponseWriter, r *http.Request, ps httprouter.Params) {
	path := ps.ByName("year")

	if !strings.HasSuffix(path, ".json") {
		log.Printf("year %q", path)
		http.Error(w, "Not Found", 404)
		return
	}

	cacheTTL := time.Minute * 15
	switch path {
	case "search_index_2018_2021.json":
		cacheTTL = time.Hour * 24
	}

	rc, err := a.getFile(r.Context(), fmt.Sprintf("build/%s", path))
	if err != nil {
		if err == storage.ErrObjectNotExist {
			a.addExpireHeaders(w, time.Minute*10)
			http.Error(w, "Not Found", 404)
			return
		}
		log.Printf("err %#v", err)
		http.Error(w, "error", 500)
		return
	}
	w.Header().Add("content-type", "application/json")
	a.addExpireHeaders(w, cacheTTL)

	_, err = io.Copy(w, rc)
	if err != nil {
		log.Printf("%#v", err)
		http.Error(w, "error", 500)
	}
}

// L1 handles /:file
//
// Redirects are cached for the lifetime of the process but not persisted
func (a *App) L1(w http.ResponseWriter, r *http.Request, ps httprouter.Params) {

	file := ps.ByName("file")
	switch file {
	case "robots.txt":
		a.RobotsTXT(w, r, ps)
		return
	case "local-laws":
		a.LocalLaws(w, r, ps)
		return
	case "councilmembers":
		a.Councilmembers(w, r, ps)
		return
	case "recent":
		a.RecentLegislation(w, r, ps)
		return
	}
	if !IsValidFileNumber(file) {
		http.Error(w, "Not Found", 404)
		return
	}
	a.IntroRedirect(w, r, ps)
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
		templateFS:      content,
		fileCache:       make(map[string]CachedFile),
	}
	if *devMode {
		app.templateFS = os.DirFS(".")
		app.staticHandler = http.StripPrefix("/static/", http.FileServer(http.Dir("static")))
	}
	app.legistar.LookupURL, err = url.Parse("https://legistar.council.nyc.gov/gateway.aspx?m=l&id=")
	if err != nil {
		panic(err)
	}

	router := httprouter.New()
	router.GET("/", app.Search)
	router.GET("/:file/:year", app.L2)
	router.GET("/:file", app.L1)

	// Determine port for HTTP service.
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	var h http.Handler = newI18nMiddleware(router)
	if *logRequests {
		h = handlers.LoggingHandler(os.Stdout, h)
	}

	// Start HTTP server.
	log.Printf("listening on port %s", port)
	if err := http.ListenAndServe(":"+port, h); err != nil {
		log.Fatal(err)
	}
}
