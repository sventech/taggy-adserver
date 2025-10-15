package main

import (
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"net/http"

	//"os"
	//"path/filepath"
	"sync"
	"time"
)

// Ad represents either a text or image ad
type Ad struct {
	ID       string `json:"id"`
	Type     string `json:"type"`    // "text" or "image"
	Content  string `json:"content"` // for text ads
	ImageURL string `json:"image_url,omitempty"`
}

// Memory store (for demo only)
var (
	ads    []Ad
	mu     sync.RWMutex
	imgDir = "ads"
)

func init() {
	rand.Seed(time.Now().UnixNano())

	// Add some demo ads
	ads = append(ads, Ad{ID: "1", Type: "text", Content: "Try our Go microframework today!"})
	ads = append(ads, Ad{ID: "2", Type: "text", Content: "Super fast and lightweight backend in Go!"})
}

func main() {
	// Serve static image ads
	fs := http.FileServer(http.Dir(imgDir))
	http.Handle("/ads/", http.StripPrefix("/ads/", fs))

	// Dynamic endpoints
	http.HandleFunc("/api/ad/random", handleRandomAd)
	http.HandleFunc("/api/ad/add", handleAddAd)

	// Start server
	addr := ":8080"
	fmt.Printf("Ad server running on http://localhost%s\n", addr)
	log.Fatal(http.ListenAndServe(addr, nil))
}

// handleRandomAd returns a random ad (text or image)
func handleRandomAd(w http.ResponseWriter, r *http.Request) {
	mu.RLock()
	defer mu.RUnlock()

	if len(ads) == 0 {
		http.Error(w, "no ads available", http.StatusNotFound)
		return
	}

	ad := ads[rand.Intn(len(ads))]

	// Randomly include image ads from the /ads/ folder
	if ad.Type == "image" {
		ad.ImageURL = fmt.Sprintf("/ads/%s", ad.ImageURL)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(ad)
}

// handleAddAd allows posting new ads via JSON
func handleAddAd(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "use POST", http.StatusMethodNotAllowed)
		return
	}

	var newAd Ad
	if err := json.NewDecoder(r.Body).Decode(&newAd); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	if newAd.ID == "" {
		newAd.ID = fmt.Sprintf("%d", rand.Int())
	}

	mu.Lock()
	ads = append(ads, newAd)
	mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok", "id": newAd.ID})
}
