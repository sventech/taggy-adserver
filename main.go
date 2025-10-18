package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

type Ad struct {
	ID          int      `json:"id"`
	AdType      string   `json:"ad_type"`
	Content     string   `json:"content,omitempty"`
	ImageURL    string   `json:"image_url,omitempty"`
	RedirectURL string   `json:"redirect_url"`
	Tags        []string `json:"tags,omitempty"`
	CampaignID  int      `json:"campaign_id,omitempty"`
	ExpiresAt   *string  `json:"expires_at,omitempty"`
}

type Campaign struct {
	ID        int    `json:"id"`
	Name      string `json:"name"`
	CreatedAt string `json:"created_at"`
}

type Impression struct {
	ID         int    `json:"id"`
	AdID       int    `json:"ad_id"`
	ActionType string `json:"action_type"` // "view" or "click"
	IP         string `json:"ip"`
	UserAgent  string `json:"user_agent"`
	ViewedAt   string `json:"viewed_at"`
}

type AnalyticsStats struct {
	AdID       int    `json:"ad_id"`
	Views      int    `json:"views"`
	Clicks     int    `json:"clicks"`
	CTR        string `json:"ctr"`
	AdType     string `json:"ad_type"`
	AdContent  string `json:"ad_content"`
	ImageURL   string `json:"image_url"`
	CampaignID int    `json:"campaign_id"`
}

// Config
const (
	dbFile             = "ads.db"
	preloadJSONFile    = "ads.json"
	preloadCampaigns   = "campaigns.json"
	preloadImpressions = "impressions.json"
	apiTokenEnvVar     = "ADSERVER_API_TOKEN"
	uploadDir          = "./static/images"
	maxUploadSize      = 10 << 20 // 10MB
)

var (
	db *sql.DB
	// Allow all origins for development (restrict in production)
	allowedOrigins = []string{"*"}
	apiToken       string
)

func main() {
	// Validate API token on startup
	apiToken = strings.TrimSpace(os.Getenv(apiTokenEnvVar))
	if apiToken == "" {
		log.Fatal("ERROR: API token not set. Set ADSERVER_API_TOKEN environment variable.")
	}

	// Ensure upload directory exists
	if err := os.MkdirAll(uploadDir, 0755); err != nil {
		log.Fatalf("Failed to create upload directory: %v", err)
	}

	var err error
	db, err = sql.Open("sqlite3", dbFile+"?_fk=1")
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	createTables()
	loadCampaignsFromJSON(preloadCampaigns)
	loadAdsFromJSON(preloadJSONFile)
	loadImpressionsFromJSON(preloadImpressions)

	mux := http.NewServeMux()

	// Public endpoints
	mux.HandleFunc("/api/ad/random", withCORS(handleRandomAd))
	mux.HandleFunc("/api/redirect/", withCORS(handleRedirect))
	mux.HandleFunc("/api/impression/", withCORS(handleImpression))
	mux.HandleFunc("/embed.js", withCORS(handleEmbedJS))

	// Protected endpoints
	mux.HandleFunc("/api/ads", withCORS(withAuth(handleListAds)))
	mux.HandleFunc("/api/ad/add", withCORS(withAuth(handleAddAd)))
	mux.HandleFunc("/api/ad/delete/", withCORS(withAuth(handleDeleteAd)))
	mux.HandleFunc("/api/ad/update/", withCORS(withAuth(handleUpdateAd)))
	mux.HandleFunc("/api/campaigns", withCORS(withAuth(handleCampaigns)))
	mux.HandleFunc("/api/campaign/add", withCORS(withAuth(handleAddCampaign)))
	mux.HandleFunc("/api/analytics/stats", withCORS(withAuth(handleAnalyticsStats)))
	mux.HandleFunc("/api/upload", withCORS(withAuth(handleUpload)))

	// Static files and admin dashboard
	mux.HandleFunc("/static/", handleStatic)
	mux.HandleFunc("/admin", handleAdmin)
	mux.HandleFunc("/", handleIndex)

	addr := ":8080"
	log.Printf("✓ Ad server running on http://localhost%s\n", addr)
	log.Printf("✓ Admin dashboard: http://localhost%s/admin\n", addr)
	log.Printf("✓ API Token: %s\n", maskToken(apiToken))
	log.Fatal(http.ListenAndServe(addr, mux))
}

func maskToken(token string) string {
	if len(token) <= 8 {
		return "****"
	}
	return token[:4] + "****" + token[len(token)-4:]
}

func createTables() {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS campaigns (
            id INTEGER PRIMARY KEY AUTOINCREMENT,
            name TEXT NOT NULL,
            created_at DATETIME DEFAULT CURRENT_TIMESTAMP
        )`,
		`CREATE TABLE IF NOT EXISTS ads (
            id INTEGER PRIMARY KEY AUTOINCREMENT,
            ad_type TEXT NOT NULL CHECK(ad_type IN ('text', 'image')),
            content TEXT,
            image_url TEXT,
            redirect_url TEXT NOT NULL,
            tags TEXT,
            campaign_id INTEGER,
            expires_at DATETIME,
            created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
            FOREIGN KEY (campaign_id) REFERENCES campaigns(id) ON DELETE SET NULL
        )`,
		`CREATE TABLE IF NOT EXISTS impressions (
            id INTEGER PRIMARY KEY AUTOINCREMENT,
            ad_id INTEGER NOT NULL,
            action_type TEXT NOT NULL CHECK(action_type IN ('view', 'click')),
            ip TEXT,
            user_agent TEXT,
            viewed_at DATETIME DEFAULT CURRENT_TIMESTAMP,
            FOREIGN KEY (ad_id) REFERENCES ads(id) ON DELETE CASCADE
        )`,
		`CREATE INDEX IF NOT EXISTS idx_ads_expires ON ads(expires_at)`,
		`CREATE INDEX IF NOT EXISTS idx_impressions_ad ON impressions(ad_id, action_type)`,
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
		log.Println("No JSON preload file found, skipping.")
		return
	}
	defer f.Close()

	var ads []Ad
	if err := json.NewDecoder(f).Decode(&ads); err != nil {
		log.Printf("Invalid JSON preload: %v", err)
		return
	}

	for _, ad := range ads {
		if err := validateAd(ad); err != nil {
			log.Printf("Skipping invalid ad: %v", err)
			continue
		}
		insertAd(ad)
	}
	log.Printf("Loaded %d ads from %s", len(ads), filename)
}

func loadCampaignsFromJSON(filename string) {
	f, err := os.Open(filename)
	if err != nil {
		log.Println("No campaigns JSON file found, skipping.")
		return
	}
	defer f.Close()

	var campaigns []Campaign
	if err := json.NewDecoder(f).Decode(&campaigns); err != nil {
		log.Printf("Invalid campaigns JSON: %v", err)
		return
	}

	for _, c := range campaigns {
		if c.Name == "" {
			log.Printf("Skipping invalid campaign with empty name")
			continue
		}
		_, err := db.Exec(`INSERT INTO campaigns (name) VALUES (?)`, c.Name)
		if err != nil {
			log.Printf("Failed to insert campaign %s: %v", c.Name, err)
			continue
		}
	}
	log.Printf("Loaded %d campaigns from %s", len(campaigns), filename)
}

func loadImpressionsFromJSON(filename string) {
	f, err := os.Open(filename)
	if err != nil {
		log.Println("No impressions JSON file found, skipping.")
		return
	}
	defer f.Close()

	var impressions []Impression
	if err := json.NewDecoder(f).Decode(&impressions); err != nil {
		log.Printf("Invalid impressions JSON: %v", err)
		return
	}

	for _, imp := range impressions {
		if imp.AdID == 0 || (imp.ActionType != "view" && imp.ActionType != "click") {
			log.Printf("Skipping invalid impression: %+v", imp)
			continue
		}
		_, err := db.Exec(`INSERT INTO impressions (ad_id, action_type, ip, user_agent, viewed_at) VALUES (?, ?, ?, ?, ?)`,
			imp.AdID, imp.ActionType, imp.IP, imp.UserAgent, imp.ViewedAt)
		if err != nil {
			log.Printf("Failed to insert impression for ad %d: %v", imp.AdID, err)
			continue
		}
	}
	log.Printf("Loaded %d impressions from %s", len(impressions), filename)
}

func validateAd(ad Ad) error {
	if ad.AdType != "text" && ad.AdType != "image" {
		return fmt.Errorf("invalid ad_type: %s", ad.AdType)
	}
	if ad.RedirectURL == "" {
		return fmt.Errorf("redirect_url is required")
	}
	if ad.AdType == "text" && ad.Content == "" {
		return fmt.Errorf("content is required for text ads")
	}
	if ad.AdType == "image" && ad.ImageURL == "" {
		return fmt.Errorf("image_url is required for image ads")
	}
	return nil
}

func insertAd(ad Ad) error {
	tags := strings.Join(ad.Tags, ",")
	var expiresAt interface{}
	if ad.ExpiresAt != nil {
		expiresAt = *ad.ExpiresAt
	}

	_, err := db.Exec(`INSERT INTO ads (ad_type, content, image_url, redirect_url, tags, campaign_id, expires_at)
                       VALUES (?, ?, ?, ?, ?, ?, ?)`,
		ad.AdType, ad.Content, ad.ImageURL, ad.RedirectURL, tags, ad.CampaignID, expiresAt)
	return err
}

// === HANDLERS ===

func handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	html := `<!DOCTYPE html>
<html>
<head><title>Ad Server</title></head>
<body style="font-family: sans-serif; max-width: 800px; margin: 50px auto; padding: 20px;">
	<h1>🎯 Ad Server</h1>
	<p>Welcome to the ad server. Available endpoints:</p>
	<ul>
		<li><a href="/admin">Admin Dashboard</a> (requires API token)</li>
		<li><code>GET /api/ad/random?tags=tech,go</code> - Get random ad</li>
		<li><code>GET /api/ads</code> - List all ads (requires auth)</li>
		<li><code>GET /embed.js</code> - Embed script for websites</li>
	</ul>
	<h3>Quick Test:</h3>
	<div id="ad-container"></div>
	<script src="/embed.js"></script>
</body>
</html>`
	w.Header().Set("Content-Type", "text/html")
	io.WriteString(w, html)
}

func handleRandomAd(w http.ResponseWriter, r *http.Request) {
	tags := strings.Split(r.URL.Query().Get("tags"), ",")

	query := `SELECT id, ad_type, content, image_url, redirect_url, tags, campaign_id 
	          FROM ads 
	          WHERE (expires_at IS NULL OR expires_at > datetime('now'))
	          ORDER BY RANDOM() LIMIT 100`

	rows, err := db.Query(query)
	if err != nil {
		respondJSON(w, http.StatusInternalServerError, map[string]string{"error": "database error"})
		return
	}
	defer rows.Close()

	var candidates []Ad
	for rows.Next() {
		var a Ad
		var tagsStr string

		err := rows.Scan(&a.ID, &a.AdType, &a.Content, &a.ImageURL, &a.RedirectURL, &tagsStr, &a.CampaignID, &a.ExpiresAt)
		if err != nil {
			continue
		}

		if tagsStr != "" {
			a.Tags = strings.Split(tagsStr, ",")
		}

		if matchesTags(a.Tags, tags) {
			candidates = append(candidates, a)
		}
	}

	if len(candidates) == 0 {
		respondJSON(w, http.StatusNotFound, map[string]string{"error": "no ads available"})
		return
	}

	ad := candidates[rand.Intn(len(candidates))]
	respondJSON(w, http.StatusOK, ad)
}

func matchesTags(adTags, userTags []string) bool {
	if len(userTags) == 0 || (len(userTags) == 1 && strings.TrimSpace(userTags[0]) == "") {
		return true
	}

	for _, ut := range userTags {
		ut = strings.TrimSpace(strings.ToLower(ut))
		if ut == "" {
			continue
		}
		for _, at := range adTags {
			at = strings.TrimSpace(strings.ToLower(at))
			if ut == at {
				return true
			}
		}
	}
	return false
}

func handleListAds(w http.ResponseWriter, r *http.Request) {
	activeOnly := r.URL.Query().Get("active") == "true"

	query := `SELECT id, ad_type, content, image_url, redirect_url, tags, campaign_id, expires_at 
	          FROM ads`
	if activeOnly {
		query += ` WHERE (expires_at IS NULL OR expires_at > datetime('now'))`
	}
	query += ` ORDER BY created_at DESC`

	rows, err := db.Query(query)
	if err != nil {
		respondJSON(w, http.StatusInternalServerError, map[string]string{"error": "database error"})
		return
	}
	defer rows.Close()

	var ads []Ad
	for rows.Next() {
		var a Ad
		var tagsStr string
		var expiresAt sql.NullString

		rows.Scan(&a.ID, &a.AdType, &a.Content, &a.ImageURL, &a.RedirectURL, &tagsStr, &a.CampaignID, &expiresAt)

		if tagsStr != "" {
			a.Tags = strings.Split(tagsStr, ",")
		}
		if expiresAt.Valid {
			a.ExpiresAt = &expiresAt.String
		}

		ads = append(ads, a)
	}

	respondJSON(w, http.StatusOK, ads)
}

func handleAddAd(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		respondJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "use POST"})
		return
	}

	var ad Ad
	if err := json.NewDecoder(r.Body).Decode(&ad); err != nil {
		respondJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}

	if err := validateAd(ad); err != nil {
		respondJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	if err := insertAd(ad); err != nil {
		respondJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to insert ad"})
		return
	}

	respondJSON(w, http.StatusCreated, map[string]string{"status": "created"})
}

func handleDeleteAd(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		respondJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "use DELETE"})
		return
	}

	idStr := strings.TrimPrefix(r.URL.Path, "/api/ad/delete/")
	id, err := strconv.Atoi(idStr)
	if err != nil {
		respondJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid ad ID"})
		return
	}

	result, err := db.Exec("DELETE FROM ads WHERE id = ?", id)
	if err != nil {
		respondJSON(w, http.StatusInternalServerError, map[string]string{"error": "database error"})
		return
	}

	rows, _ := result.RowsAffected()
	if rows == 0 {
		respondJSON(w, http.StatusNotFound, map[string]string{"error": "ad not found"})
		return
	}

	respondJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func handleUpdateAd(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		respondJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "use PUT"})
		return
	}

	idStr := strings.TrimPrefix(r.URL.Path, "/api/ad/update/")
	id, err := strconv.Atoi(idStr)
	if err != nil {
		respondJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid ad ID"})
		return
	}

	var ad Ad
	if err := json.NewDecoder(r.Body).Decode(&ad); err != nil {
		respondJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}

	if err := validateAd(ad); err != nil {
		respondJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	tags := strings.Join(ad.Tags, ",")
	var expiresAt interface{}
	if ad.ExpiresAt != nil {
		expiresAt = *ad.ExpiresAt
	}

	result, err := db.Exec(`UPDATE ads SET ad_type=?, content=?, image_url=?, redirect_url=?, tags=?, campaign_id=?, expires_at=? WHERE id=?`,
		ad.AdType, ad.Content, ad.ImageURL, ad.RedirectURL, tags, ad.CampaignID, expiresAt, id)

	if err != nil {
		respondJSON(w, http.StatusInternalServerError, map[string]string{"error": "database error"})
		return
	}

	rows, _ := result.RowsAffected()
	if rows == 0 {
		respondJSON(w, http.StatusNotFound, map[string]string{"error": "ad not found"})
		return
	}

	respondJSON(w, http.StatusOK, map[string]string{"status": "updated"})
}

func handleCampaigns(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		rows, err := db.Query(`SELECT id, name, created_at FROM campaigns ORDER BY created_at DESC`)
		if err != nil {
			respondJSON(w, http.StatusInternalServerError, map[string]string{"error": "database error"})
			return
		}
		defer rows.Close()

		var campaigns []Campaign
		for rows.Next() {
			var c Campaign
			rows.Scan(&c.ID, &c.Name, &c.CreatedAt)
			campaigns = append(campaigns, c)
		}
		respondJSON(w, http.StatusOK, campaigns)
		return
	}

	respondJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "use GET"})
}

func handleAddCampaign(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		respondJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "use POST"})
		return
	}

	var c Campaign
	if err := json.NewDecoder(r.Body).Decode(&c); err != nil {
		respondJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}

	if c.Name == "" {
		respondJSON(w, http.StatusBadRequest, map[string]string{"error": "name is required"})
		return
	}

	result, err := db.Exec(`INSERT INTO campaigns (name) VALUES (?)`, c.Name)
	if err != nil {
		respondJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to create campaign"})
		return
	}

	id, _ := result.LastInsertId()
	respondJSON(w, http.StatusCreated, map[string]interface{}{"status": "created", "id": id})
}

func handleImpression(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		respondJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "use POST"})
		return
	}

	idStr := strings.TrimPrefix(r.URL.Path, "/api/impression/")
	id, err := strconv.Atoi(idStr)
	if err != nil {
		respondJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid ad ID"})
		return
	}

	_, err = db.Exec("INSERT INTO impressions (ad_id, ad_type, ip, user_agent) VALUES (?, ?, ?, ?)",
		id, "view", r.RemoteAddr, r.UserAgent())

	if err != nil {
		respondJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to log impression"})
		return
	}

	respondJSON(w, http.StatusOK, map[string]string{"status": "logged"})
}

func handleRedirect(w http.ResponseWriter, r *http.Request) {
	idStr := strings.TrimPrefix(r.URL.Path, "/api/redirect/")
	id, err := strconv.Atoi(idStr)
	if err != nil {
		http.Error(w, "invalid ad ID", http.StatusBadRequest)
		return
	}

	var redirectURL string
	err = db.QueryRow("SELECT redirect_url FROM ads WHERE id = ?", id).Scan(&redirectURL)
	if err != nil {
		http.Error(w, "ad not found", http.StatusNotFound)
		return
	}

	_, _ = db.Exec("INSERT INTO impressions (ad_id, ad_type, ip, user_agent) VALUES (?, ?, ?, ?)",
		id, "click", r.RemoteAddr, r.UserAgent())

	http.Redirect(w, r, redirectURL, http.StatusFound)
}

func handleAnalyticsStats(w http.ResponseWriter, r *http.Request) {
	query := `
		SELECT 
			a.id,
			a.ad_type,
			a.content,
			a.image_url,
			a.campaign_id,
			COALESCE(SUM(CASE WHEN a.ad_type = 'view' THEN 1 ELSE 0 END), 0) as views,
			COALESCE(SUM(CASE WHEN a.ad_type = 'click' THEN 1 ELSE 0 END), 0) as clicks
		FROM ads a
		LEFT JOIN impressions i ON a.id = i.ad_id
		GROUP BY a.id
		ORDER BY views DESC
	`

	rows, err := db.Query(query)
	if err != nil {
		respondJSON(w, http.StatusInternalServerError, map[string]string{"error": "database error"})
		return
	}
	defer rows.Close()

	var stats []AnalyticsStats
	for rows.Next() {
		var s AnalyticsStats
		rows.Scan(&s.AdID, &s.AdType, &s.AdContent, &s.ImageURL, &s.CampaignID, &s.Views, &s.Clicks)

		if s.Views > 0 {
			ctr := float64(s.Clicks) / float64(s.Views) * 100
			s.CTR = fmt.Sprintf("%.2f%%", ctr)
		} else {
			s.CTR = "0%"
		}

		stats = append(stats, s)
	}

	respondJSON(w, http.StatusOK, stats)
}

func handleUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		respondJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "use POST"})
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxUploadSize)
	if err := r.ParseMultipartForm(maxUploadSize); err != nil {
		respondJSON(w, http.StatusBadRequest, map[string]string{"error": "file too large"})
		return
	}

	file, header, err := r.FormFile("image")
	if err != nil {
		respondJSON(w, http.StatusBadRequest, map[string]string{"error": "no file uploaded"})
		return
	}
	defer file.Close()

	// Validate file ad_type
	content_type := header.Header.Get("Content-Type")
	if !strings.HasPrefix(content_type, "image/") {
		respondJSON(w, http.StatusBadRequest, map[string]string{"error": "only images allowed"})
		return
	}

	// Generate unique filename
	ext := filepath.Ext(header.Filename)
	filename := fmt.Sprintf("%d%s", time.Now().UnixNano(), ext)
	filepath := filepath.Join(uploadDir, filename)

	dst, err := os.Create(filepath)
	if err != nil {
		respondJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to save file"})
		return
	}
	defer dst.Close()

	if _, err := io.Copy(dst, file); err != nil {
		respondJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to save file"})
		return
	}

	url := fmt.Sprintf("/static/images/%s", filename)
	respondJSON(w, http.StatusOK, map[string]string{"url": url})
}

func handleStatic(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/static/")
	filepath := filepath.Join(".", "static", path)
	http.ServeFile(w, r, filepath)
}

func handleAdmin(w http.ResponseWriter, r *http.Request) {
	// Admin dashboard HTML will be served here
	// See separate artifact for the full dashboard
	http.ServeFile(w, r, "./static/admin.html")
}

func handleEmbedJS(w http.ResponseWriter, r *http.Request) {
	js := `(function() {
  var container = document.getElementById('ad-container');
  if (!container) {
    console.error('Ad container not found');
    return;
  }

  var tags = container.getAttribute('data-tags') || '';
  var apiUrl = container.getAttribute('data-api-url') || 'http://localhost:8080';

  fetch(apiUrl + '/api/ad/random?tags=' + encodeURIComponent(tags))
    .then(function(res) { return res.json(); })
    .then(function(ad) {
      var adEl = document.createElement('div');
      adEl.style.cssText = 'border:1px solid #ddd;padding:15px;border-radius:8px;background:#f9f9f9;max-width:300px;';

      if (ad.AdType === 'text') {
        adEl.innerHTML = '<p style="margin:0;font-size:14px;">' + ad.content + '</p>';
      } else if (ad.AdType === 'image' && ad.image_url) {
        adEl.innerHTML = '<img src="' + ad.image_url + '" style="max-width:100%;height:auto;" />';
      }

      var link = document.createElement('a');
      link.href = apiUrl + '/api/redirect/' + ad.id;
      link.textContent = 'Learn More';
      link.style.cssText = 'display:inline-block;margin-top:10px;color:#0066cc;text-decoration:none;';
      link.target = '_blank';
      adEl.appendChild(link);

      container.appendChild(adEl);

      // Log impression
      fetch(apiUrl + '/api/impression/' + ad.id, { method: 'POST' });
    })
    .catch(function(err) {
      console.error('Failed to load ad:', err);
    });
})();`

	w.Header().Set("Content-Type", "application/javascript")
	w.Header().Set("Cache-Control", "no-cache")
	io.WriteString(w, js)
}

// === MIDDLEWARE ===

func withAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		authHeader := r.Header.Get("Authorization")
		token := strings.TrimPrefix(authHeader, "Bearer ")

		if token != apiToken {
			respondJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
			return
		}

		next.ServeHTTP(w, r)
	}
}

func withCORS(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")

		if origin != "" && isAllowedOrigin(origin) {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
		} else {
			w.Header().Set("Access-Control-Allow-Origin", "*")
		}

		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		w.Header().Set("Access-Control-Max-Age", "86400")

		if r.Method == http.MethodOptions {
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

// === HELPERS ===

func respondJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}
