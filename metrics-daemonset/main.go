package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
)

type LoadMetrics struct {
	Load15   float64 `json:"load15"`
	CPUCount int     `json:"cpuCount"`
}

func getLoadAverage() (float64, error) {
	data, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return 0, err
	}
	parts := strings.Fields(string(data))
	if len(parts) < 3 {
		return 0, fmt.Errorf("unexpected format in /proc/loadavg")
	}
	return strconv.ParseFloat(parts[2], 64) // 15-minute load avg
}

func getCPUCount() (int, error) {
	data, err := os.ReadFile("/proc/cpuinfo")
	if err != nil {
		return 0, err
	}
	count := 0
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "processor") {
			count++
		}
	}
	return count, nil
}

func loadHandler(w http.ResponseWriter, r *http.Request) {
	load15, err := getLoadAverage()
	if err != nil {
		http.Error(w, "failed to read loadavg", 500)
		return
	}
	cpus, err := getCPUCount()
	if err != nil {
		http.Error(w, "failed to read cpuinfo", 500)
		return
	}
	resp := LoadMetrics{Load15: load15, CPUCount: cpus}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func main() {
	http.HandleFunc("/load", loadHandler)
	fmt.Println("Listening on :9100")
	http.ListenAndServe(":9100", nil)
}
