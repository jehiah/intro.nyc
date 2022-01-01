package main

import (
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
	"regexp"
	"sort"
	"strconv"
	"strings"
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

type App struct {
	legistar *legistar.Client
	devMode  bool
	gsclient *storage.Client

	cachedRedirects map[string]string
	staticHandler   http.Handler
	templateFS      fs.FS
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

// Index returns the root path of `/`
func (a *App) Index(w http.ResponseWriter, r *http.Request, ps httprouter.Params) {
	t := newTemplate(a.templateFS, "index.html")
	w.Header().Set("content-type", "text/html")
	a.addExpireHeaders(w, time.Minute*5)
	type Page struct {
		Page string
	}
	body := Page{Page: "search"}
	err := t.ExecuteTemplate(w, "index.html", body)
	if err != nil {
		log.Print(err)
		http.Error(w, "Internal Server Error", 500)
	}
}

type LastSync struct {
	LastRun time.Time
}

type LocalLaw struct {
	File, Name, LocalLaw, Title string
	Year, LocalLawNumber        int
	LocalLawLink                template.URL
}

func (ll LocalLaw) IntroLink() template.URL {
	return template.URL("/" + strings.TrimPrefix(ll.File, "Int "))
}
func (ll LocalLaw) IntroLinkText() string {
	return "intro.nyc/" + strings.TrimPrefix(ll.File, "Int ")
}

type LocalLawYear struct {
	Year int
	Laws []LocalLaw
}

func groupLaws(l []LocalLaw) []LocalLawYear {
	years := make(map[int]*LocalLawYear)
	for _, ll := range l {
		g, ok := years[ll.Year]
		if !ok {
			g = &LocalLawYear{Year: ll.Year}
			years[ll.Year] = g
		}
		g.Laws = append(g.Laws, ll)
	}
	var groups []LocalLawYear
	for _, g := range years {
		groups = append(groups, *g)
	}
	sort.Slice(groups, func(i, j int) bool { return groups[i].Year > groups[j].Year })
	return groups
}

// LocalLaws returns the list of local laws at /local-laws
func (a *App) LocalLaws(w http.ResponseWriter, r *http.Request, ps httprouter.Params) {

	t := newTemplate(a.templateFS, "local_laws.html")

	var laws []LocalLaw
	err := a.getJSONFile(r.Context(), "build/local_laws.json", &laws)
	if err != nil {
		log.Print(err)
		http.Error(w, "Internal Server Error", 500)
	}

	g := groupLaws(laws)

	path := ps.ByName("year")
	if path == "" {
		a.addExpireHeaders(w, time.Hour)
		http.Redirect(w, r, fmt.Sprintf("/local-laws/%d", g[0].Year), 302)
		return
	}
	cacheTTL := time.Minute * 10
	if path != strconv.Itoa(time.Now().Year()) {
		cacheTTL = time.Hour * 24
	}

	type Page struct {
		Page string
		LocalLawYear
		All      []LocalLawYear
		LastSync LastSync
	}
	body := Page{
		Page: "local-laws",
		All:  g,
	}
	for _, gg := range g {
		if strconv.Itoa(gg.Year) == path {
			body.LocalLawYear = gg
			break
		}
	}
	if body.LocalLawYear.Year == 0 {
		http.Error(w, "Not Found", 404)
		return
	}

	err = a.getJSONFile(r.Context(), "build/last_sync.json", &body.LastSync)
	if err != nil {
		log.Print(err)
		http.Error(w, "Internal Server Error", 500)
	}

	w.Header().Set("content-type", "text/html")
	a.addExpireHeaders(w, cacheTTL)
	err = t.ExecuteTemplate(w, "local_laws.html", body)
	if err != nil {
		log.Print(err)
		http.Error(w, "Internal Server Error", 500)
	}
}

type Person struct {
	db.Person
	PersonMetadata
}
type PersonMetadata struct {
	ID                           int
	Twitter, TwitterPersonal     string
	Facebook, FacebookPersonal   string
	Instagram, InstagramPersonal string
}
type SocialAccount struct {
	Username string
	Link     string
	CSSClass string
}

func twitterUsername(s string) string {
	if s == "" {
		return ""
	}
	u, err := url.Parse(s)
	if err != nil {
		return ""
	}
	return "@" + strings.TrimPrefix(u.Path, "/")
}
func facebookUsername(s string) string {
	if s == "" {
		return ""
	}
	u, err := url.Parse(s)
	if err != nil {
		return ""
	}
	if strings.Contains(u.Path, "profile.php") {
		return "Facebook"
	}
	return strings.Trim(u.Path, "/")
}
func instagramUsername(s string) string {
	if s == "" {
		return ""
	}
	u, err := url.Parse(s)
	if err != nil {
		return ""
	}
	return strings.Trim(u.Path, "/")
}

func (t PersonMetadata) SocialAccounts() []SocialAccount {
	accounts := []SocialAccount{
		{twitterUsername(t.Twitter), t.Twitter, "twitter"},
		{twitterUsername(t.TwitterPersonal), t.TwitterPersonal, "twitter"},
		{facebookUsername(t.Facebook), t.Facebook, "facebook"},
		{facebookUsername(t.FacebookPersonal), t.FacebookPersonal, "facebook"},
		{instagramUsername(t.Instagram), t.Instagram, "instagram"},
		{instagramUsername(t.InstagramPersonal), t.InstagramPersonal, "instagram"},
	}
	var o []SocialAccount
	for _, a := range accounts {
		if a.Link != "" {
			o = append(o, a)
		}
	}
	return o
}

func (p Person) ActiveOfficeRecords() []db.OfficeRecord {
	var final []db.OfficeRecord
	now := time.Now()
	for _, oo := range p.OfficeRecords {
		if oo.End.Before(now) {
			continue
		}
		switch oo.BodyName {
		case "Committee of the Whole":
			continue
		case "City Council":
			continue
		case "Minority (Republican) Conference of the Council of the City of New York ":
			continue
		case "Democratic Conference of the Council of the City of New York ":
			continue
		}
		final = append(final, oo)
	}
	sort.Slice(final, func(i, j int) bool { return final[i].BodyName < final[j].BodyName })

	return final
}
func (p Person) Party() string {
	for _, oo := range p.OfficeRecords {
		switch oo.BodyName {
		case "Minority (Republican) Conference of the Council of the City of New York ":
			return "(R)"
		case "Democratic Conference of the Council of the City of New York ":
			return "(D)"
		}
	}
	return ""
}

// Councilmembers returns the list of councilmembers at /councilmembers
func (a *App) Councilmembers(w http.ResponseWriter, r *http.Request, ps httprouter.Params) {

	t := newTemplate(a.templateFS, "councilmembers.html")

	var people []db.Person
	err := a.getJSONFile(r.Context(), "build/people_active.json", &people)
	if err != nil {
		log.Print(err)
		http.Error(w, "Internal Server Error", 500)
	}

	cacheTTL := time.Minute * 30

	type Page struct {
		Page     string
		People   []Person
		LastSync LastSync
	}
	body := Page{
		Page: "councilmembers",
	}
	for _, p := range people {
		body.People = append(body.People, Person{Person: p})
	}
	var metadata []PersonMetadata
	err = a.getJSONFile(r.Context(), "build/people_metadata.json", &metadata)
	if err != nil {
		log.Print(err)
		http.Error(w, "Internal Server Error", 500)
	}
	for _, s := range metadata {
		for i, u := range body.People {
			if u.Person.ID == s.ID {
				body.People[i].PersonMetadata = s
			}
		}
	}

	err = a.getJSONFile(r.Context(), "build/last_sync.json", &body.LastSync)
	if err != nil {
		log.Print(err)
		http.Error(w, "Internal Server Error", 500)
	}

	w.Header().Set("content-type", "text/html")
	a.addExpireHeaders(w, cacheTTL)
	err = t.ExecuteTemplate(w, "councilmembers.html", body)
	if err != nil {
		log.Print(err)
		http.Error(w, "Internal Server Error", 500)
	}
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

func (l Legislation) Statuses() []Status {
	d := make(map[string]int)
	for _, ll := range l.All {
		d[ll.StatusName] += 1
	}
	var o []Status
	for n, c := range d {
		o = append(o, Status{Name: n, Count: c, Percent: (float64(c) / float64(len(l.All))) * 100})
	}
	sortSeq := []string{
		// exhaustive from MatterStatuses
		"Adopted",
		"Approved",
		"Companion Pending Approval by Council",
		"Coupled on Call-Up Vote",
		"Defeated",
		"Deferred",
		"Disapproved",
		"Discharged from Committee",
		"Failed",
		"Filed",
		"General Orders Calendar",
		"Hearing Transcripts ",
		"Local Laws",
		"Press Conference Filed",
		"Press Conference Scheduled",
		"Received, Ordered, Printed and Filed",
		"Reported from Committee and Introduced",
		"Reported from Committee",
		"Returned Unsigned by Mayor",
		"Special Event Filed",
		"Special Event Scheduled",
		"Town Hall Meeting Filed",
		"Town Hall Meeting Scheduled",

		"Introduced",
		"Committee",
		"Laid Over in Committee",
		"Filed (End of Session)",

		"Vetoed",
		"Withdrawn",
		"Enacted (Charter Referendum)",
		"Enacted (Mayor's Desk for Signature)",
		"Enacted",
	}
	sortLookup := make(map[string]int, len(sortSeq))
	for i, s := range sortSeq {
		sortLookup[s] = i
	}

	sort.Slice(o, func(i, j int) bool { return sortLookup[o[i].Name] < sortLookup[o[j].Name] })

	// TODO: sort by sequence not by string
	return o
}

type Status struct {
	Name    string
	Count   int
	Percent float64
}

func (s Status) CSSClass() string {
	return "status-" + strings.ToLower(strings.Fields(s.Name)[0])
}

// Councilmember returns the list of councilmembers at /councilmembers/$name
func (a *App) Councilmember(w http.ResponseWriter, r *http.Request, ps httprouter.Params) {
	councilmember := ps.ByName("year")
	log.Printf("Councilmember %q", councilmember)
	if matched, err := regexp.MatchString("^[a-z-]+$", councilmember); err != nil {
		log.Print(err)
		http.Error(w, "Internal Server Error", 500)
		return
	} else if !matched {
		log.Printf("council member %q not found", councilmember)
		http.Error(w, "Not Found", 404)
		return
	}

	t := newTemplate(a.templateFS, "councilmember.html")

	var people []db.Person
	err := a.getJSONFile(r.Context(), "build/people_active.json", &people)
	if err != nil {
		if err == storage.ErrObjectNotExist {
			a.addExpireHeaders(w, time.Minute*5)
			http.Error(w, "Not Found", 404)
			return
		}
		log.Print(err)
		http.Error(w, "Internal Server Error", 500)
	}
	var person db.Person
	for _, p := range people {
		if p.Slug == councilmember {
			person = p
			break
		}
	}
	if person.Slug == "" {
		log.Printf("council member %q not found", councilmember)
		a.addExpireHeaders(w, time.Minute*5)
		http.Error(w, "Not Found", 404)
		return
	}

	cacheTTL := time.Minute

	type Page struct {
		Page             string
		Person           Person
		LastSync         LastSync
		Legislation      Legislation
		PrimarySponsor   Legislation
		SecondarySponsor Legislation
	}
	body := Page{
		Page:   "councilmembers",
		Person: Person{Person: person},
	}

	err = a.getJSONFile(r.Context(), fmt.Sprintf("build/legislation_%s.json", person.Slug), &body.Legislation.All)
	if err != nil {
		// not found is ok; it means they are likely not active in current session (yet?)
		if err != storage.ErrObjectNotExist {
			log.Print(err)
			http.Error(w, "Internal Server Error", 500)
		}
	}
	body.PrimarySponsor = body.Legislation.FilterPrimarySponsor(person.ID)
	body.SecondarySponsor = body.Legislation.FilterSecondarySponsor(person.ID)

	err = a.getJSONFile(r.Context(), "build/last_sync.json", &body.LastSync)
	if err != nil {
		log.Print(err)
		http.Error(w, "Internal Server Error", 500)
	}

	w.Header().Set("content-type", "text/html")
	a.addExpireHeaders(w, cacheTTL)
	err = t.ExecuteTemplate(w, "councilmember.html", body)
	if err != nil {
		log.Print(err)
		http.Error(w, "Internal Server Error", 500)
	}
}

func (a *App) getJSONFile(ctx context.Context, filename string, v interface{}) error {
	f, err := a.getFile(ctx, filename)
	if err != nil {
		return err
	}
	defer f.Close()
	return json.NewDecoder(f).Decode(v)
}

func (a *App) getFile(ctx context.Context, filename string) (io.ReadCloser, error) {
	log.Printf("get gs://intronyc/%s", filename)
	return a.gsclient.Bucket("intronyc").Object(filename).NewReader(ctx)
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
		a.IntroJSON(w, r, ps)
		return
	}
	http.Error(w, "Not Found", 404)
	return
}

// IntroJSON proxies to /data/${year}.json to github:jehiah/nyc_legislation:introduction/$year/index.json
//
// Note: the router match pattern is `/:file/:year` so `:file` must be == "data"
func (a *App) IntroJSON(w http.ResponseWriter, r *http.Request, ps httprouter.Params) {
	path := ps.ByName("year")

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

	rc, err := a.getFile(r.Context(), fmt.Sprintf("build/%d.json", year))
	if err != nil {
		if err == storage.ErrObjectNotExist {
			a.addExpireHeaders(w, time.Minute*5)
			http.Error(w, "Not Found", 404)
			return
		}
		log.Printf("err %#v", err)
		http.Error(w, "error", 500)
		return
	}
	defer rc.Close()
	w.Header().Add("content-type", "application/json")
	a.addExpireHeaders(w, time.Minute*5)

	_, err = io.Copy(w, rc)
	if err != nil {
		log.Printf("%#v", err)
	}

}

// LocalLaw redirects to the attachment with name "Local Law ..."
// URL: /1234-2020/local-law
//
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
			a.addExpireHeaders(w, time.Hour*24*7)
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
	}
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
	router.GET("/", app.Index)
	router.GET("/:file/:year", app.L2)
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
