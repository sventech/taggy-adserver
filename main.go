package main

import (
	"database/sql"
	"encoding/json"
	"io"
	"log"
	"math/rand"
	"net/http"
	"os"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

type Ad struct {
	ID          int       `json:"id"`
	Type        string    `json:"type"`
	Content     string    `json:"content,omitempty"`
	ImageURL    string    `json:"image_url,omitempty"`
	RedirectURL string    `json:"redirect_url,omitempty"`
	Preferences []string  `json:"preferences,omitempty"`
	CampaignID  int       `json:"campaign_id,omitempty"`
	ExpiresAt   time.Time `json:"expires_at,omitempty"`
}

type Campaign struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

// Config
const (
	dbFile          = "ads.db"
	preloadJSONFile = "ads.json"
	apiTokenEnvVar  = "ADSERVER_API_TOKEN"
)

var (
	db             *sql.DB
	allowedOrigins = []string{"https://customer1.com", "https://partner.example.com"}
	apiToken       = strings.TrimSpace(os.Getenv(apiTokenEnvVar))
)

func main() {
	rand.Seed(time.Now().UnixNano())

	var err error
	db, err = sql.Open("sqlite3", dbFile)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	createTables()
	loadAdsFromJSON(preloadJSONFile)

	mux := http.NewServeMux()
	mux.HandleFunc("/api/ad/random", withCORS(handleRandomAd))
	mux.HandleFunc("/api/ad/add", withAuth(handleAddAd))
	mux.HandleFunc("/api/redirect/", handleRedirect)
	mux.HandleFunc("/api/analytics/impressions", withAuth(handleAnalytics))

	addr := ":8080"
	log.Printf("Ad server running on http://localhost%s\n", addr)
	log.Fatal(http.ListenAndServe(addr, mux))
}

// TODO: move to golang-migrate or similar
func createTables() {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS campaigns (
            id INTEGER PRIMARY KEY AUTOINCREMENT,
            name TEXT
        )`,
		`CREATE TABLE IF NOT EXISTS ads (
            id INTEGER PRIMARY KEY AUTOINCREMENT,
            type TEXT,
            content TEXT,
            image_url TEXT,
            redirect_url TEXT,
            preferences TEXT,
            campaign_id INTEGER,
            expires_at DATETIME
        )`,
		`CREATE TABLE IF NOT EXISTS impressions (
            id INTEGER PRIMARY KEY AUTOINCREMENT,
            ad_id INTEGER,
            ip TEXT,
            user_agent TEXT,
            timestamp DATETIME
        )`,
	}

	for _, stmt := range stmts {
		if _, err := db.Exec(stmt); err != nil {
			log.Fatalf("DB init error: %v", err)
		}
	}
}

func loadAdsFromJSON(filename string) {
	f, err := os.Open(filename)
	if err != nil {
		log.Println("No JSON preload file found, skipping:", err)
		return
	}
	defer f.Close()

	var ads []Ad
	if err := json.NewDecoder(f).Decode(&ads); err != nil {
		log.Println("Invalid JSON preload:", err)
		return
	}

	for _, ad := range ads {
		insertAd(ad)
	}
}

func insertAd(ad Ad) {
	prefs := strings.Join(ad.Preferences, ",")
	_, err := db.Exec(`INSERT INTO ads (type, content, image_url, redirect_url, preferences, campaign_id, expires_at)
                       VALUES (?, ?, ?, ?, ?, ?, ?)`,
		ad.Type, ad.Content, ad.ImageURL, ad.RedirectURL, prefs, ad.CampaignID, ad.ExpiresAt)
	if err != nil {
		log.Println("Insert failed:", err)
	}
}

// === HANDLERS ===

func handleRandomAd(w http.ResponseWriter, r *http.Request) {
	preferences := strings.Split(r.URL.Query().Get("preferences"), ",")

	query := `SELECT id, type, content, image_url, redirect_url, preferences FROM ads WHERE expires_at IS NULL OR expires_at > ?`
	rows, err := db.Query(query, time.Now())
	if err != nil {
		http.Error(w, "db error", 500)
		return
	}
	defer rows.Close()

	var candidates []Ad
	for rows.Next() {
		var a Ad
		var prefs string
		rows.Scan(&a.ID, &a.Type, &a.Content, &a.ImageURL, &a.RedirectURL, &prefs)
		a.Preferences = strings.Split(prefs, ",")
		if matchesPreferences(a.Preferences, preferences) {
			candidates = append(candidates, a)
		}
	}

	if len(candidates) == 0 {
		http.Error(w, "no ads available", http.StatusNotFound)
		return
	}

	ad := candidates[rand.Intn(len(candidates))]

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(ad)
}

func matchesPreferences(adPrefs, userPrefs []string) bool {
	if len(userPrefs) == 0 || (len(userPrefs) == 1 && userPrefs[0] == "") {
		return true
	}
	for _, up := range userPrefs {
		for _, ap := range adPrefs {
			if strings.EqualFold(strings.TrimSpace(up), strings.TrimSpace(ap)) {
				return true
			}
		}
	}
	return false
}

func handleAddAd(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "use POST", http.StatusMethodNotAllowed)
		return
	}

	var ad Ad
	if err := json.NewDecoder(r.Body).Decode(&ad); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	insertAd(ad)
	w.WriteHeader(http.StatusCreated)
	io.WriteString(w, `{"status":"ok"}`)
}

// Redirect and log impression
func handleRedirect(w http.ResponseWriter, r *http.Request) {
	idStr := strings.TrimPrefix(r.URL.Path, "/api/redirect/")
	var redirectURL string
	err := db.QueryRow("SELECT redirect_url FROM ads WHERE id = ?", idStr).Scan(&redirectURL)
	if err != nil {
		http.Error(w, "ad not found", 404)
		return
	}

	_, _ = db.Exec("INSERT INTO impressions (ad_id, ip, user_agent, timestamp) VALUES (?, ?, ?, ?)",
		idStr, r.RemoteAddr, r.UserAgent(), time.Now())

	http.Redirect(w, r, redirectURL, http.StatusFound)
}

// Analytics endpoint (requires token)
func handleAnalytics(w http.ResponseWriter, r *http.Request) {
	rows, err := db.Query(`SELECT ad_id, COUNT(*) FROM impressions GROUP BY ad_id`)
	if err != nil {
		http.Error(w, "db error", 500)
		return
	}
	defer rows.Close()

	stats := make(map[int]int)
	var id, count int
	for rows.Next() {
		rows.Scan(&id, &count)
		stats[id] = count
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(stats)
}

// === MIDDLEWARE ===

func withAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// FIXME: exit server earlier if no token?
		if apiToken == "" {
			http.Error(w, "missing API token", http.StatusInternalServerError)
			return
		}
		authHeader := r.Header.Get("Authorization")
		if !strings.HasPrefix(authHeader, "Bearer ") || strings.TrimPrefix(authHeader, "Bearer ") != apiToken {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		// FIXME: just next?
		next(w, r)
	}
}

func withCORS(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if isAllowedOrigin(origin) {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
		}
		next(w, r)
	}
}

func isAllowedOrigin(origin string) bool {
	for _, allowed := range allowedOrigins {
		if origin == allowed {
			return true
		}
	}
	return false
}
