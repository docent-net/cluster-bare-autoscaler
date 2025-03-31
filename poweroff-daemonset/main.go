package main

import (
	"fmt"
	"log"
	"net/http"
	"os/exec"
)

func shutdownHandler(w http.ResponseWriter, r *http.Request) {
	go func() {
		log.Println("Received shutdown request, powering off...")
		cmd := exec.Command("systemctl", "poweroff")
		if err := cmd.Run(); err != nil {
			log.Printf("Failed to power off: %v", err)
		}
	}()
	w.WriteHeader(http.StatusOK)
	fmt.Fprintln(w, "Shutting down")
}

func main() {
	http.HandleFunc("/shutdown", shutdownHandler)
	log.Println("Listening on :9101 for shutdown requests")
	http.ListenAndServe(":9101", nil)
}
