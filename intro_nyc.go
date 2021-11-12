package main

import (
        "flag"
        "log"
        "net/http"
        "os"

        "github.com/gorilla/handlers"
        "github.com/julienschmidt/httprouter"
)

type App struct {
}

func (a *App) Index(w http.ResponseWriter, r *http.Request, ps httprouter.Params) {
        http.Redirect(w, r, "https://legistar.council.nyc.gov/Legislation.aspx", 302)
}

func main() {
        logRequests := flag.Bool("log-requests", false, "log requests")
        flag.Parse()

        log.Print("starting server...")

        app := &App{}

        router := httprouter.New()
        router.GET("/", app.Index)

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
