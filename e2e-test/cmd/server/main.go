package main

import (
	"flag"
	"log"
	"net/http"

	"github.com/karl-tye/ktml/e2e-test/internal/app"
)

func main() {
	addr := flag.String("addr", ":8080", "address to listen on")
	flag.Parse()
	log.Printf("Backend listening on %s", *addr)
	if err := http.ListenAndServe(*addr, app.Routes(app.NewEnquiryHandler())); err != nil {
		log.Fatal(err)
	}
}
