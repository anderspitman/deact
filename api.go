package main

import (
	"encoding/json"
	"fmt"
	//"io"
	"net/http"
)

type EntriesResults struct {
	Entries []*DeactObject `json:"entries,omitempty"`
}

type ApiServer struct {
	db  *Database
	mux *http.ServeMux
}

func NewApiServer(db *Database) *ApiServer {

	mux := &http.ServeMux{}

	s := &ApiServer{
		db:  db,
		mux: mux,
	}

	mux.HandleFunc("/entries", s.handleEntries)

	return s
}

func (s *ApiServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

func (s *ApiServer) handleEntries(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		w.WriteHeader(405)
		fmt.Fprintf(w, "Invalid method")
		return
	}

	r.ParseForm()

	query := EntriesQuery{}

	// Default to only returning public entries (ie user has agreed to
	// publish their email address publicly).
	public := true
	publicParam := r.Form.Get("public")
	if publicParam == "false" {
		public = false
	}
	query.Public = &public

	actorParam := r.Form.Get("actor")
	if actorParam != "" {
		actor := actorParam
		query.Actor = &actor
	}

	actionParam := r.Form.Get("action")
	if actionParam != "" {
		action := actionParam
		query.Action = &action
	}

	contentParam := r.Form.Get("content")
	if contentParam == "true" {
		query.Content = true
	}

	emailParam := r.Form.Get("email")
	if emailParam == "true" {
		query.Email = true
	}

	entries, err := s.db.GetEntries(query)
	if err != nil {
		w.WriteHeader(500)
		fmt.Fprintf(w, err.Error())
		return
	}

	results := EntriesResults{
		Entries: entries,
	}

	d, err := json.MarshalIndent(results, "", "  ")
	if err != nil {
		w.WriteHeader(500)
		fmt.Fprintf(w, err.Error())
		return
	}

	w.Write(d)
}
