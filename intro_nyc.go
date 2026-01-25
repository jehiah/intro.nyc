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
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"cloud.google.com/go/storage"
	"github.com/gorilla/handlers"
	"github.com/jehiah/legislator/legistar"
)

//go:embed templates/*
var content embed.FS

//go:embed static/*
var static embed.FS

var americaNewYork, _ = time.LoadLocation("America/New_York")

type CachedLegislation struct {
	Set time.Time
	*Legislation
}

type App struct {
	legistar      *legistar.Client
	devMode       bool
	gsclient      *storage.Client
	devFilePath   string
	staticHandler http.Handler
	templateFS    fs.FS

	cachedRedirects   map[IntroID]string
	fileCache         map[string]CachedFile
	cachedLegislation map[IntroID]*CachedLegislation
	cacheMutex        sync.RWMutex
}

type CachedFile struct {
	Body []byte
	Date time.Time
}

// RobotsTXT renders /robots.txt
func (a *App) RobotsTXT(w http.ResponseWriter, r *http.Request) {
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

// ProxyJSON proxies to /data/file.json to gs://intronyc/build/$file.json
func (a *App) ProxyJSON(w http.ResponseWriter, r *http.Request) {
	path := r.PathValue("path")

	if !strings.HasSuffix(path, ".json") {
		http.Error(w, "Not Found", 404)
		return
	}

	cacheTTL := time.Minute * 15
	switch path {
	case "search_index_2022-2023.json", "search_index_2018-2021.json", "search_index_2024-2025.json":
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

func redirect(p string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u := &url.URL{Path: p, RawQuery: r.URL.RawQuery}
		http.Redirect(w, r, u.String(), 301)
	}
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
		legistar:      legistar.NewClient("nyc", os.Getenv("NYC_LEGISLATOR_TOKEN")),
		gsclient:      client,
		devMode:       *devMode,
		devFilePath:   *devFilePath,
		staticHandler: http.FileServer(http.FS(static)),
		templateFS:    content,

		cachedRedirects:   make(map[IntroID]string),
		cachedLegislation: make(map[IntroID]*CachedLegislation),
		fileCache:         make(map[string]CachedFile),
	}
	if *devMode {
		app.templateFS = os.DirFS(".")
		app.staticHandler = http.StripPrefix("/static/", http.FileServer(http.Dir("static")))
	}
	app.legistar.LookupURL, err = url.Parse("https://legistar.council.nyc.gov/gateway.aspx?m=l&id=")
	if err != nil {
		panic(err)
	}

	fileRouter := http.NewServeMux()
	fileRouter.HandleFunc("GET /{file}", app.FileRedirect)
	fileRouter.HandleFunc("GET /{file}/local-law", app.LocalLaw)

	router := http.NewServeMux()

	router.HandleFunc("GET /{$}", app.Search)
	router.HandleFunc("GET /.well-known/atproto-did", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "text/plain")
		w.Write([]byte("did:plc:42n7xtt43jwp5ukmuubb2mmo\n"))
	})
	router.HandleFunc("GET /robots.txt", app.RobotsTXT)
	router.HandleFunc("GET /recent", app.RecentLegislation)
	router.HandleFunc("GET /map", app.Map)
	router.HandleFunc("GET /calendar", app.Events)
	router.HandleFunc("GET /events", app.Events)
	router.HandleFunc("GET /events.ics", app.Events)
	router.HandleFunc("GET /councilmembers", app.Councilmembers)
	router.HandleFunc("GET /councilmembers/{councilmember}", app.Councilmember)
	router.HandleFunc("GET /local-laws", app.LocalLaws)
	router.HandleFunc("GET /local-laws/{year}", app.LocalLaws)
	router.HandleFunc("GET /data/{path}", app.ProxyJSON)
	router.HandleFunc("GET /reports/", redirect("/reports/session")) // redirect -> /reports/session
	router.Handle("GET /static/", app.staticHandler)

	router.HandleFunc("GET /reports/most_sponsored", app.ReportMostSponsored)
	router.HandleFunc("GET /reports/status", redirect("/reports/session")) // /reports/session
	router.HandleFunc("GET /reports/session", app.ReportBySession)
	router.HandleFunc("GET /reports/similarity", app.ReportSimilarity)
	router.HandleFunc("GET /reports/councilmembers", app.ReportCouncilmembers)
	router.HandleFunc("GET /reports/committees", app.ReportCommittees)
	router.HandleFunc("GET /reports/attendance", app.ReportAttendance)
	router.HandleFunc("GET /reports/reintroductions", app.ReportReintroductions)
	router.HandleFunc("GET /reports/resubmit", redirect("/reports/reintroductions"))

	router.Handle("/", fileRouter)

	// Determine port for HTTP service.
	port := os.Getenv("PORT")
	if port == "" {
		if *devMode {
			port = "443"
		} else {
			port = "8080"
		}
	}

	var h http.Handler = newI18nMiddleware(router)
	if *logRequests {
		h = handlers.LoggingHandler(os.Stdout, h)
	}

	if *devMode {
		// mkcert -key-file dev/key.pem -cert-file dev/cert.pem dev.intro.nyc
		if _, err := os.Stat("dev/cert.pem"); os.IsNotExist(err) {
			log.Printf("dev/cert.pem missing.")
			os.Mkdir("dev", 0750)
			cmd := exec.Command("mkcert", "-install", "-key-file=dev/key.pem", "-cert-file=dev/cert.pem", "dev.intro.nyc")
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
			log.Printf("%s %s", cmd.Path, strings.Join(cmd.Args[1:], " "))
			err := cmd.Run()
			if err != nil {
				log.Fatal(err)
			}
		}
		log.Printf("listening to HTTPS on port %s https://dev.intro.nyc", port)
		if err := http.ListenAndServeTLS(":"+port, "dev/cert.pem", "dev/key.pem", h); err != nil {
			log.Fatal(err)
		}
	} else {
		log.Printf("listening on port %s", port)
		if err := http.ListenAndServe(":"+port, h); err != nil {
			log.Fatal(err)
		}
	}
}
