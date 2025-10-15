package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// Config
const (
	dbFile         = "ads.db"
	adsJSONFile    = "ads.json"
	apiTokenEnvVar = "ADSERVER_API_TOKEN"
)

var (
	db *sql.DB
	mu sync.Mutex
)

// Ad struct for JSON import/export
type Ad struct {
	ID          int       `json:"id,omitempty"`
	CampaignID  int       `json:"campaign_id,omitempty"`
	Type        string    `json:"type"`
	Content     string    `json:"content,omitempty"`
	ImageURL    string    `json:"image_url,omitempty"`
	RedirectURL string    `json:"redirect_url,omitempty"`
	Tags        []string  `json:"tags,omitempty"`
	ExpiresAt   time.Time `json:"expires_at,omitempty"`
}

func main() {
	rand.Seed(time.Now().UnixNano())
	var err error

	// Initialize DB
	db, err = sql.Open("sqlite3", dbFile)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	if err := initDB(); err != nil {
		log.Fatalf("DB init error: %v", err)
	}

	// Load ads from JSON if provided
	if _, err := os.Stat(adsJSONFile); err == nil {
		if err := loadAdsFromJSON(); err != nil {
			log.Printf("Warning: could not import ads.json: %v", err)
		}
	}

	http.HandleFunc("/api/ad/random", handleRandomAd)
	http.HandleFunc("/api/ad/add", requireAuth(handleAddAd))
	http.HandleFunc("/api/ad/click", handleAdClick)
	http.HandleFunc("/api/ad/reload", requireAuth(handleReloadFromJSON))

	addr := ":8080"
	fmt.Printf("Ad server running on http://localhost%s\n", addr)
	log.Fatal(http.ListenAndServe(addr, nil))
}

// ---------- DB Setup ----------

func initDB() error {
	schema := `
CREATE TABLE IF NOT EXISTS campaigns (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS ads (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    campaign_id INTEGER REFERENCES campaigns(id),
    type TEXT,
    content TEXT,
    image_url TEXT,
    redirect_url TEXT,
    tags TEXT,
    expires_at DATETIME
);

CREATE TABLE IF NOT EXISTS impressions (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    ad_id INTEGER REFERENCES ads(id),
    timestamp DATETIME DEFAULT CURRENT_TIMESTAMP,
    ip TEXT
);
`
	_, err := db.Exec(schema)
	return err
}

// ---------- JSON import ----------

func loadAdsFromJSON() error {
	f, err := os.Open(adsJSONFile)
	if err != nil {
		return err
	}
	defer f.Close()

	var ads []Ad
	if err := json.NewDecoder(f).Decode(&ads); err != nil {
		return err
	}

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`
        INSERT INTO ads (campaign_id, type, content, image_url, redirect_url, tags, expires_at)
        VALUES (?, ?, ?, ?, ?, ?, ?)
    `)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, ad := range ads {
		tagsJSON, _ := json.Marshal(ad.Tags)
		var exp *string
		if !ad.ExpiresAt.IsZero() {
			formatted := ad.ExpiresAt.Format(time.RFC3339)
			exp = &formatted
		}
		_, err := stmt.Exec(ad.CampaignID, ad.Type, ad.Content, ad.ImageURL, ad.RedirectURL, string(tagsJSON), exp)
		if err != nil {
			log.Printf("Failed to import ad: %v", err)
		}
	}

	return tx.Commit()
}

func handleReloadFromJSON(w http.ResponseWriter, r *http.Request) {
	if err := loadAdsFromJSON(); err != nil {
		http.Error(w, "reload failed: "+err.Error(), 500)
		return
	}
	w.Write([]byte("Reloaded ads from JSON"))
}

// ---------- Auth Middleware ----------

func requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := os.Getenv(apiTokenEnvVar)
		if token == "" {
			http.Error(w, "missing API token", 500)
			return
		}
		authHeader := r.Header.Get("Authorization")
		if !strings.HasPrefix(authHeader, "Bearer ") || strings.TrimPrefix(authHeader, "Bearer ") != token {
			http.Error(w, "unauthorized", 401)
			return
		}
		next.ServeHTTP(w, r)
	}
}

// ---------- Handlers ----------

// /api/ad/random?preferences=a,b,c
func handleRandomAd(w http.ResponseWriter, r *http.Request) {
	prefs := []string{}
	if q := r.URL.Query().Get("preferences"); q != "" {
		prefs = strings.Split(q, ",")
	}

	query := "SELECT id, type, content, image_url, redirect_url, tags, expires_at FROM ads"
	rows, err := db.Query(query)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	defer rows.Close()

	var available []Ad
	now := time.Now()
	for rows.Next() {
		var ad Ad
		var tagsStr, expStr sql.NullString
		if err := rows.Scan(&ad.ID, &ad.Type, &ad.Content, &ad.ImageURL, &ad.RedirectURL, &tagsStr, &expStr); err != nil {
			continue
		}

		if expStr.Valid {
			t, err := time.Parse(time.RFC3339, expStr.String)
			if err == nil && t.Before(now) {
				continue // expired
			}
		}

		if tagsStr.Valid {
			json.Unmarshal([]byte(tagsStr.String), &ad.Tags)
		}

		if len(prefs) == 0 || anyMatch(ad.Tags, prefs) {
			available = append(available, ad)
		}
	}

	if len(available) == 0 {
		http.Error(w, "no ads available", 404)
		return
	}

	ad := available[rand.Intn(len(available))]
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(ad)
}

// /api/ad/add
func handleAddAd(w http.ResponseWriter, r *http.Request) {
	var ad Ad
	if err := json.NewDecoder(r.Body).Decode(&ad); err != nil {
		http.Error(w, "invalid JSON", 400)
		return
	}

	tagsJSON, _ := json.Marshal(ad.Tags)
	var exp *string
	if !ad.ExpiresAt.IsZero() {
		formatted := ad.ExpiresAt.Format(time.RFC3339)
		exp = &formatted
	}

	_, err := db.Exec(`
        INSERT INTO ads (campaign_id, type, content, image_url, redirect_url, tags, expires_at)
        VALUES (?, ?, ?, ?, ?, ?, ?)`,
		ad.CampaignID, ad.Type, ad.Content, ad.ImageURL, ad.RedirectURL, string(tagsJSON), exp)
	if err != nil {
		http.Error(w, "DB insert error: "+err.Error(), 500)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

// /api/ad/click?id=123
func handleAdClick(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	if id == "" {
		http.Error(w, "missing id", 400)
		return
	}

	var redirectURL string
	err := db.QueryRow("SELECT redirect_url FROM ads WHERE id = ?", id).Scan(&redirectURL)
	if err != nil {
		http.Error(w, "ad not found", 404)
		return
	}

	ip, _, _ := net.SplitHostPort(r.RemoteAddr)
	_, _ = db.Exec("INSERT INTO impressions (ad_id, ip) VALUES (?, ?)", id, ip)

	if redirectURL == "" {
		http.Error(w, "no redirect for this ad", 404)
		return
	}
	http.Redirect(w, r, redirectURL, http.StatusFound)
}

// ---------- Utils ----------

func anyMatch(adTags, prefs []string) bool {
	for _, a := range adTags {
		for _, p := range prefs {
			if strings.EqualFold(strings.TrimSpace(a), strings.TrimSpace(p)) {
				return true
			}
		}
	}
	return false
}
