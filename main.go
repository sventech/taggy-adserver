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

// Configuration
const (
	adsFile        = "ads.json"
	apiTokenEnvVar = "ADSERVER_API_TOKEN"
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
	ads []Ad
	mu  sync.RWMutex
)

// Allowed origins for GET requests
var allowedOrigins = []string{
	"https://your-frontend-site.com",
	"https://partner1.com",
	"http://localhost:8000",
}

func main() {
	rand.Seed(time.Now().UnixNano())

	if err := loadAds(); err != nil {
		log.Fatalf("failed to load ads: %v", err)
	}

	http.HandleFunc("/api/ad/random", withCORS(handleRandomAd))
	http.HandleFunc("/api/ad/add", requireAuth(handleAddAd))
	http.HandleFunc("/api/ad/reload", requireAuth(withCORS(handleReloadAds)))

	addr := ":8080"
	fmt.Printf("Ad server running on http://localhost%s\n", addr)
	log.Fatal(http.ListenAndServe(addr, nil))
}

// ---------- Core logic ----------

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

func saveAds() error {
	mu.RLock()
	defer mu.RUnlock()

	data, err := json.MarshalIndent(ads, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(adsFile, data, 0644)
}

// ---------- Middleware ----------

// requireAuth protects endpoints with API token
func requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := os.Getenv(apiTokenEnvVar)
		if token == "" {
			http.Error(w, "server misconfigured: missing ADSERVER_API_TOKEN", http.StatusInternalServerError)
			return
		}

		authHeader := r.Header.Get("Authorization")
		if !strings.HasPrefix(authHeader, "Bearer ") || strings.TrimPrefix(authHeader, "Bearer ") != token {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		next.ServeHTTP(w, r)
	}
}

// withCORS allows certain origins for GET requests
func withCORS(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")

		if origin != "" && isAllowedOrigin(origin) {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
		}

		if r.Method == http.MethodOptions {
			w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next.ServeHTTP(w, r)
	}
}

func isAllowedOrigin(o string) bool {
	for _, allowed := range allowedOrigins {
		if strings.EqualFold(o, allowed) {
			return true
		}
	}
	return false
}

// ---------- Handlers ----------

func handleReloadAds(w http.ResponseWriter, r *http.Request) {
	if err := loadAds(); err != nil {
		http.Error(w, "failed to reload ads: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Write([]byte("reloaded ads"))
}

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
		pool = ads
	}

	if len(pool) == 0 {
		http.Error(w, "no ads available", http.StatusNotFound)
		return
	}

	ad := pool[rand.Intn(len(pool))]

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(ad)
}

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
