package main

import (
	"log"
	"net/http"
	"os"
)

func main() {
	store := NewConfigStore()
	guardian := NewGuardian(store)
	guardian.Start()

	addr := getenv("LISTEN_ADDR")
	if addr == "" {
		addr = ":5080"
	}

	log.Printf("listening on %s", addr)
	if err := http.ListenAndServe(addr, newApp(store, guardian)); err != nil {
		log.Fatal(err)
	}
}

func getenv(key string) string {
	return os.Getenv(key)
}
