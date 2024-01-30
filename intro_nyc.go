package main

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"cloud.google.com/go/storage"
	"github.com/gorilla/handlers"
	"github.com/jehiah/legislator/legistar"
	"github.com/julienschmidt/httprouter"
)

//go:embed templates/*
var content embed.FS

//go:embed static/*
var static embed.FS

var americaNewYork, _ = time.LoadLocation("America/New_York")

type App struct {
	legistar    *legistar.Client
	devMode     bool
	gsclient    *storage.Client
	devFilePath string

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

// RobotsTXT renders /robots.txt
func (a *App) RobotsTXT(w http.ResponseWriter, r *http.Request, ps httprouter.Params) {
	w.Header().Set("content-type", "text/plain")
	a.addExpireHeaders(w, time.Hour*24*7)
	io.WriteString(w, "# robots welcome\n# https://github.com/jehiah/intro.nyc\n")
}

type LastSync struct {
	LastRun time.Time
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

	var body []byte
	if a.devMode && a.devFilePath != "" {
		fp := filepath.Join(a.devFilePath, filename)
		log.Printf("opening %s", fp)
		f, err := os.Open(fp)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		body, err = io.ReadAll(f)
		if err != nil {
			return nil, err
		}
	} else {
		log.Printf("get gs://intronyc/%s", filename)
		r, err := a.gsclient.Bucket("intronyc").Object(filename).NewReader(ctx)
		if err != nil {
			return nil, err
		}
		body, err = io.ReadAll(r)
		if err != nil {
			return nil, err
		}
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
	switch {
	case path == "local-law" && IsValidFileNumber(file):
		a.LocalLaw(w, r, file)
	case file == "local-laws":
		a.LocalLaws(w, r, ps)
	case file == "councilmembers":
		a.Councilmember(w, r, ps)
	case file == "static":
		a.staticHandler.ServeHTTP(w, r)
	case file == "data":
		a.ProxyJSON(w, r, ps)
	case file == "reports":
		ps = append(ps, httprouter.Param{Key: "report", Value: path})
		a.Reports(w, r, ps)
	default:
		http.Error(w, "Not Found", 404)
	}
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
	case "search_index_2018-2021.json":
		cacheTTL = time.Hour * 24
	}

	rc, err := a.getFile(r.Context(), fmt.Sprintf("build/%s", path))
	if err != nil {
		if err == storage.ErrObjectNotExist || os.IsNotExist(err) {
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
		return
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
	case "reports":
		a.Reports(w, r, ps)
		return
	case "map":
		a.Map(w, r, ps)
		return
	case "events", "calendar":
		a.Events(w, r, ps)
		return
	}
	if IsValidFileNumber(file) {
		a.IntroRedirect(w, r, ps)
	}
	if strings.HasSuffix(file, ".json") && IsValidFileNumber(strings.TrimSuffix(file, ".json")) {
		a.IntroJSON(w, r, ps)
	}
	http.Error(w, "Not Found", 404)
	return
}

func main() {
	logRequests := flag.Bool("log-requests", false, "log requests")
	devMode := flag.Bool("dev-mode", false, "development mode")
	devFilePath := flag.String("file-path", "", "path to files normally retrieved from gs://intronyc/")
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
		devFilePath:     *devFilePath,
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
