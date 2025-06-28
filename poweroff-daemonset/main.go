package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
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

func findMainInterfaceAndMAC() (string, string, error) {
	data, err := os.ReadFile("/proc/net/route")
	if err != nil {
		return "", "", fmt.Errorf("reading route table: %w", err)
	}

	var mainIface string
	lines := strings.Split(string(data), "\n")
	for _, line := range lines[1:] {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[1] == "00000000" {
			mainIface = fields[0]
			break
		}
	}

	if mainIface == "" {
		return "", "", fmt.Errorf("could not determine main interface from /proc/net/route")
	}

	iface, err := net.InterfaceByName(mainIface)
	if err != nil {
		return "", "", fmt.Errorf("getting interface %s: %w", mainIface, err)
	}

	if iface.HardwareAddr == nil {
		return "", "", fmt.Errorf("interface %s has no MAC address", mainIface)
	}

	return mainIface, iface.HardwareAddr.String(), nil
}

func macHandler(w http.ResponseWriter, r *http.Request) {
	iface, mac, err := findMainInterfaceAndMAC()
	if err != nil {
		http.Error(w, "error: "+err.Error(), http.StatusInternalServerError)
		log.Println("[/mac] Failed:", err)
		return
	}

	resp := map[string]string{
		"interface": iface,
		"mac":       mac,
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func main() {
	http.HandleFunc("/shutdown", shutdownHandler)
	http.HandleFunc("/mac", macHandler)
	log.Println("Listening on :9101 for requests")
	if err := http.ListenAndServe(":9101", nil); err != nil {
		log.Fatalf("ListenAndServe failed: %v", err)
	}
}
