package main

import (
	"fmt"
	"log"
	"net"
	"net/http"
)

const shutdownSocket = "/run/cba-shutdown.sock"

func shutdownHandler(w http.ResponseWriter, r *http.Request) {
	go func() {
		log.Println("Received shutdown request, sending to systemd socket...")

		conn, err := net.Dial("unix", shutdownSocket)
		if err != nil {
			log.Printf("Failed to dial systemd socket: %v", err)
			return
		}
		defer conn.Close()

		_, _ = conn.Write([]byte("shutdown\n"))
	}()

	w.WriteHeader(http.StatusOK)
	fmt.Fprintln(w, "Shutdown signal sent via systemd socket")
}

func main() {
	http.HandleFunc("/shutdown", shutdownHandler)
	log.Println("Listening on :9101 for shutdown requests")
	if err := http.ListenAndServe(":9101", nil); err != nil {
		log.Fatalf("ListenAndServe failed: %v", err)
	}
}
