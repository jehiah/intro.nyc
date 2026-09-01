package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// The editor reads law from the nyc_code_archive repository, which files every
// provision as one JSON document under the path its citation implies. See that
// repository's README for the section format.
//
// In development the archive is a local checkout; in production it is mirrored
// to gs://intronyc/law/ and fetched through App.getFile.

type lawDataset struct {
	Dataset string `json:"dataset"`
	Code    string `json:"code"`
	Label   string `json:"label"`

	// From the dataset's manifest.json.
	CurrentThrough string `json:"current_through,omitempty"`
	Sections       int    `json:"sections,omitempty"`
	Generated      string `json:"generated,omitempty"`
}

var lawDatasets = []lawDataset{
	{Dataset: "administrative-code", Code: "administrative code", Label: "Administrative Code"},
	{Dataset: "charter", Code: "charter", Label: "New York City Charter"},
	{Dataset: "rules", Code: "rules", Label: "Rules of the City of New York"},
}

func lookupDataset(name string) (lawDataset, bool) {
	for _, d := range lawDatasets {
		if d.Dataset == name {
			return d, true
		}
	}
	return lawDataset{}, false
}

func titleCase(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// LawSectionRef is one entry of a dataset's table of contents.
type LawSectionRef struct {
	Dataset string `json:"dataset"`
	Code    string `json:"code"`
	Cite    string `json:"cite"`
	Heading string `json:"heading"`
	File    string `json:"file"`
	Path    string `json:"path"`
}

// lawIndexNode is a node of a dataset's index.json.
type lawIndexNode struct {
	Level      string         `json:"level"`
	Designator string         `json:"designator"`
	Heading    string         `json:"heading"`
	Cite       string         `json:"cite"`
	File       string         `json:"file"`
	Children   []lawIndexNode `json:"children"`
}

type lawIndex struct {
	sections []LawSectionRef
	loaded   time.Time
}

const lawIndexTTL = time.Hour

// lawFile reads one file from the archive.
func (a *App) lawFile(ctx context.Context, name string) ([]byte, error) {
	if a.lawPath != "" {
		return os.ReadFile(filepath.Join(a.lawPath, filepath.FromSlash(name)))
	}
	r, err := a.getFile(ctx, "law/"+name)
	if err != nil {
		return nil, err
	}
	return io.ReadAll(r)
}

// lawIndexFor returns the flattened table of contents for a dataset, loading it
// on first use. The index files are large, so they are parsed once and the
// flattened form is kept rather than re-read per request.
func (a *App) lawIndexFor(ctx context.Context, dataset lawDataset) ([]LawSectionRef, error) {
	a.lawMutex.RLock()
	cached, ok := a.lawIndexes[dataset.Dataset]
	a.lawMutex.RUnlock()
	if ok && time.Since(cached.loaded) < lawIndexTTL {
		return cached.sections, nil
	}

	body, err := a.lawFile(ctx, dataset.Dataset+"/index.json")
	if err != nil {
		return nil, err
	}
	var nodes []lawIndexNode
	if err := json.Unmarshal(body, &nodes); err != nil {
		return nil, fmt.Errorf("%s/index.json: %w", dataset.Dataset, err)
	}

	var sections []LawSectionRef
	var walk func(nodes []lawIndexNode, ancestors []string)
	walk = func(nodes []lawIndexNode, ancestors []string) {
		for _, n := range nodes {
			if n.Level == "section" && n.File != "" {
				sections = append(sections, LawSectionRef{
					Dataset: dataset.Dataset,
					Code:    dataset.Code,
					Cite:    n.Cite,
					Heading: n.Heading,
					File:    n.File,
					Path:    strings.Join(ancestors, " \u203a "),
				})
				continue
			}
			label := titleCase(n.Level)
			if n.Designator != "" {
				label += " " + n.Designator
			}
			walk(n.Children, append(ancestors, label))
		}
	}
	walk(nodes, nil)

	a.lawMutex.Lock()
	a.lawIndexes[dataset.Dataset] = &lawIndex{sections: sections, loaded: time.Now()}
	a.lawMutex.Unlock()
	return sections, nil
}

// EditorLawDatasets lists the bodies of law available, with the publisher's
// currency statement so a drafter can see how current the text is.
func (a *App) EditorLawDatasets(w http.ResponseWriter, r *http.Request) {
	out := make([]lawDataset, 0, len(lawDatasets))
	for _, d := range lawDatasets {
		body, err := a.lawFile(r.Context(), d.Dataset+"/manifest.json")
		if err != nil {
			log.Printf("law manifest %s: %s", d.Dataset, err)
			continue
		}
		var manifest struct {
			CurrentThrough string `json:"current_through"`
			Sections       int    `json:"sections"`
			Generated      string `json:"generated"`
		}
		if err := json.Unmarshal(body, &manifest); err == nil {
			d.CurrentThrough = manifest.CurrentThrough
			d.Sections = manifest.Sections
			d.Generated = manifest.Generated
		}
		out = append(out, d)
	}
	w.Header().Set("content-type", "application/json")
	a.addExpireHeaders(w, time.Hour)
	json.NewEncoder(w).Encode(map[string]any{"datasets": out})
}

// score ranks a section against a query. Higher is better; 0 means no match.
func score(ref LawSectionRef, query string) int {
	cite := strings.ToLower(ref.Cite)
	heading := strings.ToLower(ref.Heading)
	switch {
	case cite == query:
		return 100
	case strings.HasPrefix(cite, query):
		return 80 - len(cite)
	case strings.HasPrefix(heading, query):
		return 50
	case strings.Contains(heading, query):
		return 30
	case strings.Contains(cite, query):
		return 10
	}
	return 0
}

// EditorLawSearch finds sections by citation or heading.
func (a *App) EditorLawSearch(w http.ResponseWriter, r *http.Request) {
	query := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
	limit := 25
	if n := r.URL.Query().Get("limit"); n != "" {
		fmt.Sscanf(n, "%d", &limit)
	}
	if limit < 1 || limit > 200 {
		limit = 25
	}

	// A bill amends consolidated law; agency rules are searchable but are not
	// offered by default (Rule 5.2).
	requested := r.URL.Query()["dataset"]
	if len(requested) == 0 {
		requested = []string{"administrative-code", "charter"}
	}

	type scored struct {
		ref   LawSectionRef
		score int
	}
	var hits []scored

	for _, name := range requested {
		dataset, ok := lookupDataset(name)
		if !ok {
			http.Error(w, "Bad Request", 400)
			return
		}
		sections, err := a.lawIndexFor(r.Context(), dataset)
		if err != nil {
			log.Printf("law index %s: %s", name, err)
			http.Error(w, "Internal Server Error", 500)
			return
		}
		for _, ref := range sections {
			if query == "" {
				continue
			}
			if s := score(ref, query); s > 0 {
				hits = append(hits, scored{ref, s})
			}
		}
	}

	sort.SliceStable(hits, func(i, j int) bool { return hits[i].score > hits[j].score })
	if len(hits) > limit {
		hits = hits[:limit]
	}
	results := make([]LawSectionRef, 0, len(hits))
	for _, h := range hits {
		results = append(results, h.ref)
	}

	w.Header().Set("content-type", "application/json")
	a.addExpireHeaders(w, time.Minute*15)
	json.NewEncoder(w).Encode(map[string]any{"results": results})
}

// EditorLawSection serves one provision straight from the archive.
func (a *App) EditorLawSection(w http.ResponseWriter, r *http.Request) {
	dataset, ok := lookupDataset(r.PathValue("dataset"))
	if !ok {
		http.Error(w, "Not Found", 404)
		return
	}

	// The archive is a static tree, so the request names a file. Everything
	// below is about making sure it names a file inside the dataset.
	rel := path.Clean("/" + r.PathValue("path"))[1:]
	if rel == "" || !strings.HasSuffix(rel, ".json") || strings.HasPrefix(rel, "../") {
		http.Error(w, "Not Found", 404)
		return
	}

	body, err := a.lawFile(r.Context(), path.Join(dataset.Dataset, rel))
	if err != nil {
		if os.IsNotExist(err) {
			http.Error(w, "Not Found", 404)
			return
		}
		log.Printf("law section %s/%s: %s", dataset.Dataset, rel, err)
		http.Error(w, "Internal Server Error", 500)
		return
	}

	w.Header().Set("content-type", "application/json")
	a.addExpireHeaders(w, time.Hour)
	w.Write(body)
}

// lawArchiveSource describes where law is being read from, for the startup log.
func lawArchiveSource(localPath string) string {
	if localPath != "" {
		return localPath
	}
	return "gs://intronyc/law/"
}
