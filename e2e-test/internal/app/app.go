package app

import (
	"bytes"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"sync"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

type Enquiry struct {
	Id       string
	Customer string
	Summary  string
	Status   string
}

type EnquiryStore struct {
	mu        sync.Mutex
	enquiries []Enquiry
	nextID    int
}

func NewEnquiryStore() *EnquiryStore {
	return &EnquiryStore{
		enquiries: []Enquiry{
			{Id: "1", Customer: "Ada Lovelace", Summary: "Boiler pressure is dropping", Status: "new"},
			{Id: "2", Customer: "Grace Hopper", Summary: "Annual service is due", Status: "new"},
			{Id: "3", Customer: "Margaret Hamilton", Summary: "Quote accepted", Status: "converted"},
		},
		nextID: 4,
	}
}

func (s *EnquiryStore) List() []Enquiry {
	s.mu.Lock()
	defer s.mu.Unlock()
	enquiries := make([]Enquiry, len(s.enquiries))
	copy(enquiries, s.enquiries)
	return enquiries
}

func (s *EnquiryStore) create(customer, summary string) (Enquiry, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	enquiry := Enquiry{
		Id:       fmt.Sprintf("%d", s.nextID),
		Customer: customer,
		Summary:  summary,
		Status:   "new",
	}
	s.nextID++
	s.enquiries = append([]Enquiry{enquiry}, s.enquiries...)
	return enquiry, len(s.enquiries)
}

func (s *EnquiryStore) convert(id string) (Enquiry, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.enquiries {
		if s.enquiries[i].Id == id {
			s.enquiries[i].Status = "converted"
			return s.enquiries[i], true
		}
	}
	return Enquiry{}, false
}

type Renderer struct{}

func (Renderer) templates(files ...string) (*template.Template, error) {
	return template.ParseFiles(files...)
}

func (renderer Renderer) page(w http.ResponseWriter, count int) {
	tmpl, err := renderer.templates("index.html")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := tmpl.ExecuteTemplate(w, "html", map[string]string{"Count": fmt.Sprintf("%d", count)}); err != nil {
		log.Printf("render page: %v", err)
	}
}

func (renderer Renderer) component(w http.ResponseWriter, name string, data any) {
	tmpl, err := renderer.templates("components.html")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := tmpl.ExecuteTemplate(w, name, data); err != nil {
		log.Printf("render component %s: %v", name, err)
	}
}

type EnquiryHandler struct {
	Store    *EnquiryStore
	Renderer Renderer
}

func NewEnquiryHandler() EnquiryHandler {
	return EnquiryHandler{Store: NewEnquiryStore(), Renderer: Renderer{}}
}

func (handler EnquiryHandler) dashboard(w http.ResponseWriter, r *http.Request) {
	handler.Renderer.page(w, len(handler.Store.List()))
}

func (handler EnquiryHandler) list(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("HX-Request") != "true" {
		handler.dashboard(w, r)
		return
	}
	tmpl, err := handler.Renderer.templates("components.html")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	enquiries := handler.Store.List()
	var output bytes.Buffer
	for _, enquiry := range enquiries {
		if err := tmpl.ExecuteTemplate(&output, "EnquiryRow", enquiry); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
	_ = tmpl.ExecuteTemplate(&output, "EnquiryCount", map[string]string{"Count": fmt.Sprintf("%d", len(enquiries))})
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(output.Bytes())
}

func (handler EnquiryHandler) create(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	enquiry, count := handler.Store.create(r.FormValue("customer"), r.FormValue("summary"))
	tmpl, err := handler.Renderer.templates("components.html")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	var output bytes.Buffer
	_ = tmpl.ExecuteTemplate(&output, "EnquiryRow", enquiry)
	_ = tmpl.ExecuteTemplate(&output, "EnquiryCount", map[string]string{"Count": fmt.Sprintf("%d", count)})
	_ = tmpl.ExecuteTemplate(&output, "EnquiryInputs", map[string]string{})
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(output.Bytes())
}

func (handler EnquiryHandler) convert(w http.ResponseWriter, r *http.Request) {
	enquiry, ok := handler.Store.convert(chi.URLParam(r, "id"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	handler.Renderer.component(w, "EnquiryRow", enquiry)
}

func Routes(handler EnquiryHandler) http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.Recoverer)
	r.Get("/", handler.dashboard)
	r.Get("/static/htmx.min.js", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, "htmx.min.js")
	})
	r.Get("/enquiries", handler.list)
	r.Post("/enquiries", handler.create)
	r.Route("/enquiries/{id}", func(r chi.Router) {
		r.Put("/status", handler.convert)
	})
	return r
}
