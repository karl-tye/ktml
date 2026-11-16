// Command server is the dashboard application of the HMR browser test.
package main

import (
	"flag"
	"log"
	"net/http"

	"example.com/hmrapp/internal/app"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:8080", "address to listen on")
	csp := flag.String("csp", "", "Content-Security-Policy header sent with every response")
	flag.Parse()
	handler := devHandler(app.Routes())
	if *csp != "" {
		routes := handler
		handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Security-Policy", *csp)
			routes.ServeHTTP(w, r)
		})
	}
	log.Printf("hmrapp listening on %s", *addr)
	log.Fatal(http.ListenAndServe(*addr, handler))
}
