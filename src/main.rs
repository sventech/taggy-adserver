use actix_cors::Cors;
use actix_files::Files;
use actix_multipart::Multipart;
use actix_web::{
    delete, get, middleware, post, put, web, App, HttpRequest, HttpResponse, HttpServer,
    Result as ActixResult,
};
use futures_util::stream::StreamExt;
use rand::seq::IndexedRandom;
use serde::{Deserialize, Serialize};
use sqlx::sqlite::{SqliteConnectOptions, SqlitePool};
use sqlx::Row;
use std::env;
use std::fs;
use std::io::Write;
use std::path::PathBuf;
use std::str::FromStr;

// ============================================================================
// Models
// ============================================================================

#[derive(Debug, Serialize, Deserialize, Clone)]
struct Ad {
    #[serde(skip_deserializing)]
    id: Option<i64>,
    ad_type: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    content: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    image_url: Option<String>,
    redirect_url: String,
    #[serde(default)]
    tags: Vec<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    campaign_id: Option<i64>,
    #[serde(skip_serializing_if = "Option::is_none")]
    expires_at: Option<String>,
}

#[derive(Debug, Serialize, Deserialize)]
struct Campaign {
    #[serde(skip_deserializing)]
    id: Option<i64>,
    name: String,
    #[serde(skip_deserializing)]
    created_at: Option<String>,
}

#[derive(Debug, Serialize, Deserialize)]
struct Impression {
    ad_id: i64,
    action_type: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    ip: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    user_agent: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    viewed_at: Option<String>,
}

#[derive(Debug, Serialize)]
struct AnalyticsStats {
    ad_id: i64,
    views: i64,
    clicks: i64,
    ctr: String,
    ad_type: String,
    ad_content: String,
    image_url: String,
    campaign_id: Option<i64>,
}

#[derive(Debug, Serialize)]
struct ErrorResponse {
    error: String,
}

#[derive(Debug, Serialize)]
struct StatusResponse {
    status: String,
}

#[derive(Debug, Serialize)]
struct UploadResponse {
    url: String,
}

// ============================================================================
// Application State
// ============================================================================

struct AppState {
    db: SqlitePool,
    api_token: String,
    upload_dir: PathBuf,
}

// ============================================================================
// Database Initialization
// ============================================================================

async fn init_db(pool: &SqlitePool) -> Result<(), sqlx::Error> {
    sqlx::query(
        r#"
        CREATE TABLE IF NOT EXISTS campaigns (
            id INTEGER PRIMARY KEY AUTOINCREMENT,
            name TEXT NOT NULL,
            created_at DATETIME DEFAULT CURRENT_TIMESTAMP
        )
        "#,
    )
    .execute(pool)
    .await?;

    sqlx::query(
        r#"
        CREATE TABLE IF NOT EXISTS ads (
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
        )
        "#,
    )
    .execute(pool)
    .await?;

    sqlx::query(
        r#"
        CREATE TABLE IF NOT EXISTS impressions (
            id INTEGER PRIMARY KEY AUTOINCREMENT,
            ad_id INTEGER NOT NULL,
            action_type TEXT NOT NULL CHECK(action_type IN ('view', 'click')),
            ip TEXT,
            user_agent TEXT,
            viewed_at DATETIME DEFAULT CURRENT_TIMESTAMP,
            FOREIGN KEY (ad_id) REFERENCES ads(id) ON DELETE CASCADE
        )
        "#,
    )
    .execute(pool)
    .await?;

    sqlx::query("CREATE INDEX IF NOT EXISTS idx_ads_expires ON ads(expires_at)")
        .execute(pool)
        .await?;

    sqlx::query("CREATE INDEX IF NOT EXISTS idx_impressions_ad ON impressions(ad_id, action_type)")
        .execute(pool)
        .await?;

    Ok(())
}

async fn load_campaigns_from_json(pool: &SqlitePool, filename: &str) {
    let Ok(contents) = fs::read_to_string(filename) else {
        log::info!("No campaigns JSON file found, skipping.");
        return;
    };

    let Ok(campaigns) = serde_json::from_str::<Vec<Campaign>>(&contents) else {
        log::warn!("Invalid campaigns JSON format");
        return;
    };

    for campaign in campaigns {
        if campaign.name.is_empty() {
            continue;
        }

        let _ = sqlx::query("INSERT INTO campaigns (name) VALUES (?)")
            .bind(&campaign.name)
            .execute(pool)
            .await;
    }

    log::info!("Loaded campaigns from {}", filename);
}

async fn load_ads_from_json(pool: &SqlitePool, filename: &str) {
    let Ok(contents) = fs::read_to_string(filename) else {
        log::info!("No ads JSON file found, skipping.");
        return;
    };

    let Ok(ads) = serde_json::from_str::<Vec<Ad>>(&contents) else {
        log::warn!("Invalid ads JSON format");
        return;
    };

    for ad in ads {
        if let Err(e) = validate_ad(&ad) {
            log::warn!("Skipping invalid ad: {}", e);
            continue;
        }

        let tags = ad.tags.join(",");
        let _ = sqlx::query(
            "INSERT INTO ads (ad_type, content, image_url, redirect_url, tags, campaign_id, expires_at) VALUES (?, ?, ?, ?, ?, ?, ?)"
        )
        .bind(&ad.ad_type)
        .bind(&ad.content)
        .bind(&ad.image_url)
        .bind(&ad.redirect_url)
        .bind(&tags)
        .bind(&ad.campaign_id)
        .bind(&ad.expires_at)
        .execute(pool)
        .await;
    }

    log::info!("Loaded ads from {}", filename);
}

async fn load_impressions_from_json(pool: &SqlitePool, filename: &str) {
    let Ok(contents) = fs::read_to_string(filename) else {
        log::info!("No impressions JSON file found, skipping.");
        return;
    };

    let Ok(impressions) = serde_json::from_str::<Vec<Impression>>(&contents) else {
        log::warn!("Invalid impressions JSON format");
        return;
    };

    for imp in impressions {
        if imp.action_type != "view" && imp.action_type != "click" {
            continue;
        }

        let _ = sqlx::query(
            "INSERT INTO impressions (ad_id, action_type, ip, user_agent, viewed_at) VALUES (?, ?, ?, ?, ?)"
        )
        .bind(imp.ad_id)
        .bind(&imp.action_type)
        .bind(&imp.ip)
        .bind(&imp.user_agent)
        .bind(&imp.viewed_at)
        .execute(pool)
        .await;
    }

    log::info!("Loaded impressions from {}", filename);
}

// ============================================================================
// Validation
// ============================================================================

fn validate_ad(ad: &Ad) -> Result<(), String> {
    if ad.ad_type != "text" && ad.ad_type != "image" {
        return Err(format!("invalid ad_type: {}", ad.ad_type));
    }
    if ad.redirect_url.is_empty() {
        return Err("redirect_url is required".to_string());
    }
    if ad.ad_type == "text" && ad.content.as_ref().map_or(true, |c| c.is_empty()) {
        return Err("content is required for text ads".to_string());
    }
    if ad.ad_type == "image" && ad.image_url.as_ref().map_or(true, |u| u.is_empty()) {
        return Err("image_url is required for image ads".to_string());
    }
    Ok(())
}

fn matches_tags(ad_tags: &[String], user_tags: &[String]) -> bool {
    if user_tags.is_empty() || (user_tags.len() == 1 && user_tags[0].trim().is_empty()) {
        return true;
    }

    for user_tag in user_tags {
        let ut = user_tag.trim().to_lowercase();
        if ut.is_empty() {
            continue;
        }
        for ad_tag in ad_tags {
            let at = ad_tag.trim().to_lowercase();
            if ut == at {
                return true;
            }
        }
    }
    false
}

fn mask_token(token: &str) -> String {
    if token.len() <= 8 {
        return "****".to_string();
    }
    format!("{}****{}", &token[..4], &token[token.len() - 4..])
}

// ============================================================================
// Middleware
// ============================================================================

async fn auth_middleware(
    req: HttpRequest,
    state: web::Data<AppState>,
) -> Result<(), actix_web::Error> {
    let auth_header = req
        .headers()
        .get("Authorization")
        .and_then(|h| h.to_str().ok())
        .unwrap_or("");

    let token = auth_header.strip_prefix("Bearer ").unwrap_or("");

    if token != state.api_token {
        return Err(actix_web::error::ErrorUnauthorized("unauthorized"));
    }

    Ok(())
}

// ============================================================================
// Handlers - Public
// ============================================================================

#[get("/")]
async fn index() -> ActixResult<HttpResponse> {
    let html = r#"<!DOCTYPE html>
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
</html>"#;

    Ok(HttpResponse::Ok()
        .content_type("text/html")
        .body(html))
}

#[get("/api/ad/random")]
async fn get_random_ad(
    state: web::Data<AppState>,
    query: web::Query<std::collections::HashMap<String, String>>,
) -> ActixResult<HttpResponse> {
    let tags_str = query.get("tags").map(|s| s.as_str()).unwrap_or("");
    let user_tags: Vec<String> = tags_str
        .split(',')
        .map(|s| s.to_string())
        .collect();

    let rows = sqlx::query(
        r#"
        SELECT id, ad_type, content, image_url, redirect_url, tags, campaign_id, expires_at
        FROM ads
        WHERE (expires_at IS NULL OR expires_at > datetime('now'))
        ORDER BY RANDOM()
        LIMIT 100
        "#,
    )
    .fetch_all(&state.db)
    .await
    .map_err(|_| actix_web::error::ErrorInternalServerError("database error"))?;

    let mut candidates = Vec::new();

    for row in rows {
        let tags_str: String = row.try_get("tags").unwrap_or_default();
        let ad_tags: Vec<String> = if tags_str.is_empty() {
            Vec::new()
        } else {
            tags_str.split(',').map(|s| s.to_string()).collect()
        };

        if matches_tags(&ad_tags, &user_tags) {
            let ad = Ad {
                id: row.try_get("id").ok(),
                ad_type: row.try_get("ad_type").unwrap_or_default(),
                content: row.try_get("content").ok(),
                image_url: row.try_get("image_url").ok(),
                redirect_url: row.try_get("redirect_url").unwrap_or_default(),
                tags: ad_tags,
                campaign_id: row.try_get("campaign_id").ok(),
                expires_at: row.try_get("expires_at").ok(),
            };
            candidates.push(ad);
        }
    }

    if candidates.is_empty() {
        return Ok(HttpResponse::NotFound().json(ErrorResponse {
            error: "no ads available".to_string(),
        }));
    }

    let mut rng = rand::rng();
    let ad = candidates.choose(&mut rng).unwrap();

    Ok(HttpResponse::Ok().json(ad))
}

#[get("/api/redirect/{id}")]
async fn redirect_ad(
    state: web::Data<AppState>,
    path: web::Path<i64>,
    req: HttpRequest,
) -> ActixResult<HttpResponse> {
    let id = path.into_inner();

    let row = sqlx::query("SELECT redirect_url FROM ads WHERE id = ?")
        .bind(id)
        .fetch_one(&state.db)
        .await
        .map_err(|_| actix_web::error::ErrorNotFound("ad not found"))?;

    let redirect_url: String = row.try_get("redirect_url").unwrap_or_default();

    let ip = req
        .connection_info()
        .realip_remote_addr()
        .unwrap_or("unknown")
        .to_string();
    let user_agent = req
        .headers()
        .get("user-agent")
        .and_then(|h| h.to_str().ok())
        .unwrap_or("unknown")
        .to_string();

    let _ = sqlx::query(
        "INSERT INTO impressions (ad_id, action_type, ip, user_agent) VALUES (?, ?, ?, ?)",
    )
    .bind(id)
    .bind("click")
    .bind(&ip)
    .bind(&user_agent)
    .execute(&state.db)
    .await;

    Ok(HttpResponse::Found()
        .append_header(("Location", redirect_url))
        .finish())
}

#[post("/api/impression/{id}")]
async fn log_impression(
    state: web::Data<AppState>,
    path: web::Path<i64>,
    req: HttpRequest,
) -> ActixResult<HttpResponse> {
    let id = path.into_inner();

    let ip = req
        .connection_info()
        .realip_remote_addr()
        .unwrap_or("unknown")
        .to_string();
    let user_agent = req
        .headers()
        .get("user-agent")
        .and_then(|h| h.to_str().ok())
        .unwrap_or("unknown")
        .to_string();

    sqlx::query(
        "INSERT INTO impressions (ad_id, action_type, ip, user_agent) VALUES (?, ?, ?, ?)",
    )
    .bind(id)
    .bind("view")
    .bind(&ip)
    .bind(&user_agent)
    .execute(&state.db)
    .await
    .map_err(|_| actix_web::error::ErrorInternalServerError("failed to log impression"))?;

    Ok(HttpResponse::Ok().json(StatusResponse {
        status: "logged".to_string(),
    }))
}

#[get("/embed.js")]
async fn embed_js() -> ActixResult<HttpResponse> {
    let js = r#"(function() {
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

      if (ad.ad_type === 'text') {
        adEl.innerHTML = '<p style="margin:0;font-size:14px;">' + ad.content + '</p>';
      } else if (ad.ad_type === 'image' && ad.image_url) {
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
})();"#;

    Ok(HttpResponse::Ok()
        .content_type("application/javascript")
        .append_header(("Cache-Control", "no-cache"))
        .body(js))
}

#[get("/admin")]
async fn admin_dashboard() -> ActixResult<actix_files::NamedFile> {
    Ok(actix_files::NamedFile::open("./static/admin.html")?)
}

// ============================================================================
// Handlers - Protected
// ============================================================================

#[get("/api/ads")]
async fn list_ads(
    state: web::Data<AppState>,
    req: HttpRequest,
    query: web::Query<std::collections::HashMap<String, String>>,
) -> ActixResult<HttpResponse> {
    auth_middleware(req, state.clone()).await?;

    let active_only = query.get("active").map(|s| s == "true").unwrap_or(false);

    let sql = if active_only {
        "SELECT id, ad_type, content, image_url, redirect_url, tags, campaign_id, expires_at FROM ads WHERE (expires_at IS NULL OR expires_at > datetime('now')) ORDER BY created_at DESC"
    } else {
        "SELECT id, ad_type, content, image_url, redirect_url, tags, campaign_id, expires_at FROM ads ORDER BY created_at DESC"
    };

    let rows = sqlx::query(sql)
        .fetch_all(&state.db)
        .await
        .map_err(|_| actix_web::error::ErrorInternalServerError("database error"))?;

    let mut ads = Vec::new();
    for row in rows {
        let tags_str: String = row.try_get("tags").unwrap_or_default();
        let tags: Vec<String> = if tags_str.is_empty() {
            Vec::new()
        } else {
            tags_str.split(',').map(|s| s.to_string()).collect()
        };

        let ad = Ad {
            id: row.try_get("id").ok(),
            ad_type: row.try_get("ad_type").unwrap_or_default(),
            content: row.try_get("content").ok(),
            image_url: row.try_get("image_url").ok(),
            redirect_url: row.try_get("redirect_url").unwrap_or_default(),
            tags,
            campaign_id: row.try_get("campaign_id").ok(),
            expires_at: row.try_get("expires_at").ok(),
        };
        ads.push(ad);
    }

    Ok(HttpResponse::Ok().json(ads))
}

#[post("/api/ad/add")]
async fn add_ad(
    state: web::Data<AppState>,
    req: HttpRequest,
    ad: web::Json<Ad>,
) -> ActixResult<HttpResponse> {
    auth_middleware(req, state.clone()).await?;

    validate_ad(&ad).map_err(|e| actix_web::error::ErrorBadRequest(e))?;

    let tags = ad.tags.join(",");

    sqlx::query(
        "INSERT INTO ads (ad_type, content, image_url, redirect_url, tags, campaign_id, expires_at) VALUES (?, ?, ?, ?, ?, ?, ?)"
    )
    .bind(&ad.ad_type)
    .bind(&ad.content)
    .bind(&ad.image_url)
    .bind(&ad.redirect_url)
    .bind(&tags)
    .bind(&ad.campaign_id)
    .bind(&ad.expires_at)
    .execute(&state.db)
    .await
    .map_err(|_| actix_web::error::ErrorInternalServerError("failed to insert ad"))?;

    Ok(HttpResponse::Created().json(StatusResponse {
        status: "created".to_string(),
    }))
}

#[delete("/api/ad/delete/{id}")]
async fn delete_ad(
    state: web::Data<AppState>,
    req: HttpRequest,
    path: web::Path<i64>,
) -> ActixResult<HttpResponse> {
    auth_middleware(req, state.clone()).await?;

    let id = path.into_inner();

    let result = sqlx::query("DELETE FROM ads WHERE id = ?")
        .bind(id)
        .execute(&state.db)
        .await
        .map_err(|_| actix_web::error::ErrorInternalServerError("database error"))?;

    if result.rows_affected() == 0 {
        return Ok(HttpResponse::NotFound().json(ErrorResponse {
            error: "ad not found".to_string(),
        }));
    }

    Ok(HttpResponse::Ok().json(StatusResponse {
        status: "deleted".to_string(),
    }))
}

#[put("/api/ad/update/{id}")]
async fn update_ad(
    state: web::Data<AppState>,
    req: HttpRequest,
    path: web::Path<i64>,
    ad: web::Json<Ad>,
) -> ActixResult<HttpResponse> {
    auth_middleware(req, state.clone()).await?;

    let id = path.into_inner();

    validate_ad(&ad).map_err(|e| actix_web::error::ErrorBadRequest(e))?;

    let tags = ad.tags.join(",");

    let result = sqlx::query(
        "UPDATE ads SET ad_type=?, content=?, image_url=?, redirect_url=?, tags=?, campaign_id=?, expires_at=? WHERE id=?"
    )
    .bind(&ad.ad_type)
    .bind(&ad.content)
    .bind(&ad.image_url)
    .bind(&ad.redirect_url)
    .bind(&tags)
    .bind(&ad.campaign_id)
    .bind(&ad.expires_at)
    .bind(id)
    .execute(&state.db)
    .await
    .map_err(|_| actix_web::error::ErrorInternalServerError("database error"))?;

    if result.rows_affected() == 0 {
        return Ok(HttpResponse::NotFound().json(ErrorResponse {
            error: "ad not found".to_string(),
        }));
    }

    Ok(HttpResponse::Ok().json(StatusResponse {
        status: "updated".to_string(),
    }))
}

#[get("/api/campaigns")]
async fn list_campaigns(
    state: web::Data<AppState>,
    req: HttpRequest,
) -> ActixResult<HttpResponse> {
    auth_middleware(req, state.clone()).await?;

    let rows = sqlx::query("SELECT id, name, created_at FROM campaigns ORDER BY created_at DESC")
        .fetch_all(&state.db)
        .await
        .map_err(|_| actix_web::error::ErrorInternalServerError("database error"))?;

    let mut campaigns = Vec::new();
    for row in rows {
        let campaign = Campaign {
            id: row.try_get("id").ok(),
            name: row.try_get("name").unwrap_or_default(),
            created_at: row.try_get("created_at").ok(),
        };
        campaigns.push(campaign);
    }

    Ok(HttpResponse::Ok().json(campaigns))
}

#[post("/api/campaign/add")]
async fn add_campaign(
    state: web::Data<AppState>,
    req: HttpRequest,
    campaign: web::Json<Campaign>,
) -> ActixResult<HttpResponse> {
    auth_middleware(req, state.clone()).await?;

    if campaign.name.is_empty() {
        return Ok(HttpResponse::BadRequest().json(ErrorResponse {
            error: "name is required".to_string(),
        }));
    }

    let result = sqlx::query("INSERT INTO campaigns (name) VALUES (?)")
        .bind(&campaign.name)
        .execute(&state.db)
        .await
        .map_err(|_| actix_web::error::ErrorInternalServerError("failed to create campaign"))?;

    Ok(HttpResponse::Created().json(serde_json::json!({
        "status": "created",
        "id": result.last_insert_rowid()
    })))
}

#[get("/api/analytics/stats")]
async fn analytics_stats(
    state: web::Data<AppState>,
    req: HttpRequest,
) -> ActixResult<HttpResponse> {
    auth_middleware(req, state.clone()).await?;

    let rows = sqlx::query(
        r#"
        SELECT 
            a.id,
            a.ad_type,
            a.content,
            a.image_url,
            a.campaign_id,
            COALESCE(SUM(CASE WHEN i.action_type = 'view' THEN 1 ELSE 0 END), 0) as views,
            COALESCE(SUM(CASE WHEN i.action_type = 'click' THEN 1 ELSE 0 END), 0) as clicks
        FROM ads a
        LEFT JOIN impressions i ON a.id = i.ad_id
        GROUP BY a.id
        ORDER BY views DESC
        "#,
    )
    .fetch_all(&state.db)
    .await
    .map_err(|_| actix_web::error::ErrorInternalServerError("database error"))?;

    let mut stats = Vec::new();
    for row in rows {
        let views: i64 = row.try_get("views").unwrap_or(0);
        let clicks: i64 = row.try_get("clicks").unwrap_or(0);

        let ctr = if views > 0 {
            format!("{:.2}%", (clicks as f64 / views as f64) * 100.0)
        } else {
            "0%".to_string()
        };

        let stat = AnalyticsStats {
            ad_id: row.try_get("id").unwrap_or(0),
            views,
            clicks,
            ctr,
            ad_type: row.try_get("ad_type").unwrap_or_default(),
            ad_content: row.try_get("content").unwrap_or_default(),
            image_url: row.try_get("image_url").unwrap_or_default(),
            campaign_id: row.try_get("campaign_id").ok(),
        };
        stats.push(stat);
    }

    Ok(HttpResponse::Ok().json(stats))
}

#[post("/api/upload")]
async fn upload_file(
    state: web::Data<AppState>,
    req: HttpRequest,
    mut payload: Multipart,
) -> ActixResult<HttpResponse> {
    auth_middleware(req, state.clone()).await?;

    while let Some(item) = payload.next().await {
        let mut field = item?;

        let content_disposition = field.content_disposition();
        let filename = content_disposition
            .ok_or_else(|| actix_web::error::ErrorBadRequest("missing content disposition"))?
            .get_filename()
            .ok_or_else(|| actix_web::error::ErrorBadRequest("no filename"))?;

        let content_type = field.content_type();
        let content_type = content_type.ok_or_else(|| actix_web::error::ErrorBadRequest("missing content type"))?;
        if !content_type.type_().as_str().starts_with("image") {
            return Ok(HttpResponse::BadRequest().json(ErrorResponse {
                error: "only images allowed".to_string(),
            }));
        }

        let ext = std::path::Path::new(filename)
            .extension()
            .and_then(|e| e.to_str())
            .unwrap_or("jpg");

        let new_filename = format!("{}.{}", chrono::Utc::now().timestamp_nanos_opt().unwrap_or(0), ext);
        let filepath = state.upload_dir.join(&new_filename);

        let mut file = std::fs::File::create(&filepath)
            .map_err(|_| actix_web::error::ErrorInternalServerError("failed to create file"))?;

        while let Some(chunk) = field.next().await {
            let data = chunk?;
            file.write_all(&data)
                .map_err(|_| actix_web::error::ErrorInternalServerError("failed to write file"))?;
        }

        let url = format!("/static/images/{}", new_filename);
        return Ok(HttpResponse::Ok().json(UploadResponse { url }));
    }

    Ok(HttpResponse::BadRequest().json(ErrorResponse {
        error: "no file uploaded".to_string(),
    }))
}

// ============================================================================
// Main
// ============================================================================

#[actix_web::main]
async fn main() -> std::io::Result<()> {
    env_logger::init_from_env(env_logger::Env::new().default_filter_or("info"));

    // Validate API token
    let api_token = env::var("ADSERVER_API_TOKEN")
        .expect("ERROR: API token not set. Set ADSERVER_API_TOKEN environment variable.");
    let api_token = api_token.trim().to_string();

    if api_token.is_empty() {
        panic!("ERROR: API token cannot be empty");
    }

    // Setup upload directory
    let upload_dir = PathBuf::from("./static/images");
    fs::create_dir_all(&upload_dir).expect("Failed to create upload directory");

    // Connect to database
    let db_options = SqliteConnectOptions::from_str("sqlite:ads.db?mode=rwc")
        .expect("Invalid database URL")
        .foreign_keys(true);

    let pool = SqlitePool::connect_with(db_options)
        .await
        .expect("Failed to connect to database");

    // Initialize database
    init_db(&pool).await.expect("Failed to initialize database");

    // Load data from JSON files
    load_campaigns_from_json(&pool, "campaigns.json").await;
    load_ads_from_json(&pool, "ads.json").await;
    load_impressions_from_json(&pool, "impressions.json").await;

    // Create application state
    let app_state = web::Data::new(AppState {
        db: pool,
        api_token: api_token.clone(),
        upload_dir,
    });

    let addr = "127.0.0.1:8080";

    log::info!("✓ Ad server running on http://{}", addr);
    log::info!("✓ Admin dashboard: http://{}/admin", addr);
    log::info!("✓ API Token: {}", mask_token(&api_token));

    HttpServer::new(move || {
        let cors = Cors::default()
            .allow_any_origin()
            .allow_any_method()
            .allow_any_header()
            .max_age(86400);

        App::new()
            .app_data(app_state.clone())
            .wrap(middleware::Logger::default())
            .wrap(cors)
            // Public routes
            .service(index)
            .service(get_random_ad)
            .service(redirect_ad)
            .service(log_impression)
            .service(embed_js)
            .service(admin_dashboard)
            // Protected routes
            .service(list_ads)
            .service(add_ad)
            .service(delete_ad)
            .service(update_ad)
            .service(list_campaigns)
            .service(add_campaign)
            .service(analytics_stats)
            .service(upload_file)
            // Static files
            .service(Files::new("/static", "./static"))
    })
    .bind(addr)?
    .run()
    .await
}