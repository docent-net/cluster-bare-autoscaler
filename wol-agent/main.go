package main

import (
	"bytes"
	"fmt"
	"log"
	"net"
	"net/http"
)

func wakeHandler(w http.ResponseWriter, r *http.Request) {
	mac := r.URL.Query().Get("mac")
	bcast := r.URL.Query().Get("broadcast")

	if mac == "" || bcast == "" {
		http.Error(w, "Missing mac or broadcast parameter", http.StatusBadRequest)
		return
	}

	log.Printf("Received wake request for MAC: %s via broadcast: %s", mac, bcast)

	err := sendMagicPacket(mac, bcast)
	if err != nil {
		log.Printf("Failed to send magic packet: %v", err)
		http.Error(w, "Failed to send packet", http.StatusInternalServerError)
		return
	}

	log.Printf("Magic packet sent to %s via %s", mac, bcast)
	w.WriteHeader(http.StatusOK)
	fmt.Fprintln(w, "WOL packet sent")
}

func sendMagicPacket(macAddr string, broadcastAddr string) error {
	mac, err := net.ParseMAC(macAddr)
	if err != nil {
		return fmt.Errorf("invalid MAC address: %w", err)
	}

	packet := append(bytes.Repeat([]byte{0xFF}, 6), bytes.Repeat(mac, 16)...)

	addr := &net.UDPAddr{
		IP:   net.ParseIP(broadcastAddr),
		Port: 9,
	}
	conn, err := net.DialUDP("udp", nil, addr)
	if err != nil {
		return fmt.Errorf("UDP dial error: %w", err)
	}
	defer conn.Close()

	_, err = conn.Write(packet)
	return err
}

func main() {
	http.HandleFunc("/wake", wakeHandler)
	log.Println("Listening on :9102 for WOL requests")
	log.Fatal(http.ListenAndServe(":9102", nil))
}
