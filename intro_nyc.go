package main

import (
        "flag"
        "fmt"
        "log"
        "net/http"
        "net/url"
        "os"
        "regexp"
        "strconv"
        "strings"

        "github.com/gorilla/handlers"
        "github.com/jehiah/legislator/legistar"
        "github.com/julienschmidt/httprouter"
)

type App struct {
        legistar *legistar.Client

        cachedRedirects map[string]string
}

func (a *App) Index(w http.ResponseWriter, r *http.Request, ps httprouter.Params) {
        http.Redirect(w, r, "https://legistar.council.nyc.gov/Legislation.aspx", 302)
}

func (a *App) FileRedirect(w http.ResponseWriter, r *http.Request, ps httprouter.Params) {
        file := ps.ByName("file")
        if ok, _ := regexp.MatchString("^[0-9]{4}-20(14|15|16|17|18|19|20|21|22)", file); !ok {
                http.Error(w, "Not Found", 404)
                return
        }

        n := strings.Split(file, "-")
        seq, _ := strconv.Atoi(n[0])
        if seq > 3000 {
                http.Error(w, "Not Found", 404)
                return
        }
        // year, err := strconv.Atoi(n[1])
        // if err != nil {
        //         log.Print(err)
        //         http.Error(w, "INVALID REQUEST", 400)
        //         return
        // }
        file = fmt.Sprintf("Int %s", file)

        if redirect, ok := a.cachedRedirects[file]; ok {
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

        http.Redirect(w, r, redirect, 302)
}

func main() {
        logRequests := flag.Bool("log-requests", false, "log requests")
        flag.Parse()

        log.Print("starting server...")

        app := &App{
                legistar: legistar.NewClient("nyc", os.Getenv("NYC_LEGISLATOR_TOKEN")),

                cachedRedirects: make(map[string]string),
        }
        var err error
        app.legistar.LookupURL, err = url.Parse("https://legistar.council.nyc.gov/gateway.aspx?m=l&id=")
        if err != nil {
                panic(err)
        }

        router := httprouter.New()
        router.GET("/", app.Index)
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
