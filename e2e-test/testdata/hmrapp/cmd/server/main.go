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
	flag.Parse()
	log.Printf("hmrapp listening on %s", *addr)
	log.Fatal(http.ListenAndServe(*addr, devHandler(app.Routes())))
}
