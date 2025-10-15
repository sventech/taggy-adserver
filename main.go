package main

import (
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

// Ad represents one advertisement
type Ad struct {
	ID       string   `json:"id"`
	Type     string   `json:"type"` // "text" or "image"
	Content  string   `json:"content,omitempty"`
	ImageURL string   `json:"image_url,omitempty"`
	Tags     []string `json:"tags,omitempty"`
}

var (
	adsFile = "ads.json"
	ads     []Ad
	mu      sync.RWMutex
)

func main() {
	rand.Seed(time.Now().UnixNano())

	if err := loadAds(); err != nil {
		log.Fatalf("failed to load ads: %v", err)
	}

	http.HandleFunc("/api/ad/random", handleRandomAd)
	http.HandleFunc("/api/ad/add", handleAddAd)
	http.HandleFunc("/api/ad/reload", handleReloadAds)

	addr := ":8080"
	fmt.Printf("Ad server running on http://localhost%s\n", addr)
	log.Fatal(http.ListenAndServe(addr, nil))
}

// loadAds reads ads from ads.json
func loadAds() error {
	mu.Lock()
	defer mu.Unlock()

	f, err := os.Open(adsFile)
	if err != nil {
		return err
	}
	defer f.Close()

	var data []Ad
	if err := json.NewDecoder(f).Decode(&data); err != nil {
		return err
	}

	ads = data
	log.Printf("Loaded %d ads from %s", len(ads), adsFile)
	return nil
}

// saveAds writes ads to ads.json
func saveAds() error {
	mu.RLock()
	defer mu.RUnlock()

	data, err := json.MarshalIndent(ads, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(adsFile, data, 0644)
}

// handleReloadAds reloads ads.json from disk (for easy updates)
func handleReloadAds(w http.ResponseWriter, r *http.Request) {
	if err := loadAds(); err != nil {
		http.Error(w, "failed to reload ads: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Write([]byte("reloaded ads"))
}

// handleAddAd allows adding new ads via POST (JSON)
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

	if err := saveAds(); err != nil {
		http.Error(w, "failed to save ad", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok", "id": newAd.ID})
}

// handleRandomAd returns a random ad, possibly filtered by ?preferences=
func handleRandomAd(w http.ResponseWriter, r *http.Request) {
	prefsParam := r.URL.Query().Get("preferences")
	prefs := []string{}
	if prefsParam != "" {
		prefs = strings.Split(prefsParam, ",")
	}

	mu.RLock()
	defer mu.RUnlock()

	var filtered []Ad
	if len(prefs) > 0 {
		for _, ad := range ads {
			if anyMatch(ad.Tags, prefs) {
				filtered = append(filtered, ad)
			}
		}
	}

	var pool []Ad
	if len(filtered) > 0 {
		pool = filtered
	} else {
		pool = ads // fallback: serve any ad
	}

	if len(pool) == 0 {
		http.Error(w, "no ads available", http.StatusNotFound)
		return
	}

	ad := pool[rand.Intn(len(pool))]

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(ad)
}

// anyMatch checks if any tag in adTags is in prefs
func anyMatch(adTags, prefs []string) bool {
	for _, a := range adTags {
		for _, p := range prefs {
			if strings.EqualFold(a, p) {
				return true
			}
		}
	}
	return false
}
