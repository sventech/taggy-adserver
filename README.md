# Taggy AdServer (Rust)

Simple, privacy-first advertising server with targeting based on explicit user preferences (via TAGS). Built with Rust, Actix-web, and SQLite.

## Features

- 🎯 Tag-based ad targeting (no tracking cookies or fingerprinting)
- 🔒 Token-based API authentication
- 📊 Built-in analytics (views, clicks, CTR)
- 🖼️ Support for text and image ads
- 📁 Campaign management
- 🚀 Fast and lightweight with Actix-web
- 💾 SQLite database with async queries (SQLx)

## Dependencies

This implementation uses:
- **actix-web** - Fast, pragmatic web framework
- **sqlx** - Async SQL toolkit with compile-time query checking
- **tokio** - Async runtime
- **serde/serde_json** - Serialization/deserialization
- **chrono** - Date and time handling

## Setup

1. **Install Rust** (if not already installed):
```bash
curl --proto '=https' --tlsv1.2 -sSf https://sh.rustup.rs | sh
```

2. **Clone and build**:
```bash
cargo build --release
```

3. **Create required directories**:
```bash
mkdir -p static/images
```

4. **Run the server**:
```bash
ADSERVER_API_TOKEN=mysecrettoken cargo run --release
```

The server will start on `http://localhost:8080`

## Usage Examples

### Get a random ad
```bash
curl http://localhost:8080/api/ad/random
```

### Get an ad with tag filtering
```bash
curl "http://localhost:8080/api/ad/random?tags=organic,fair-trade"
```

### Add a new ad (requires authentication)
```bash
curl -X POST http://localhost:8080/api/ad/add \
  -H "Authorization: Bearer mysecrettoken" \
  -H "Content-Type: application/json" \
  -d '{
    "campaign_id": 1,
    "ad_type": "text",
    "content": "Try our amazing product!",
    "redirect_url": "https://example.com/product",
    "tags": ["tech", "developer"],
    "expires_at": "2025-12-31T23:59:59Z"
  }'
```

### List all ads (requires authentication)
```bash
curl http://localhost:8080/api/ads \
  -H "Authorization: Bearer mysecrettoken"
```

### List only active ads
```bash
curl "http://localhost:8080/api/ads?active=true" \
  -H "Authorization: Bearer mysecrettoken"
```

### Update an ad (requires authentication)
```bash
curl -X PUT http://localhost:8080/api/ad/update/1 \
  -H "Authorization: Bearer mysecrettoken" \
  -H "Content-Type: application/json" \
  -d '{
    "ad_type": "text",
    "content": "Updated content!",
    "redirect_url": "https://example.com/new",
    "tags": ["updated"]
  }'
```

### Delete an ad (requires authentication)
```bash
curl -X DELETE http://localhost:8080/api/ad/delete/1 \
  -H "Authorization: Bearer mysecrettoken"
```

### Get analytics (requires authentication)
```bash
curl http://localhost:8080/api/analytics/stats \
  -H "Authorization: Bearer mysecrettoken"
```

### Upload an image (requires authentication)
```bash
curl -X POST http://localhost:8080/api/upload \
  -H "Authorization: Bearer mysecrettoken" \
  -F "image=@/path/to/image.jpg"
```

## API Endpoints

| Endpoint                | Method | Description                               | Auth Required | CORS    |
|------------------------|--------|-------------------------------------------|---------------|---------|
| `/`                    | GET    | Landing page                              | No            | Yes     |
| `/api/ad/random`       | GET    | Get random ad (optionally filtered)       | No            | Yes     |
| `/api/redirect/{id}`   | GET    | Redirect to ad URL (logs click)           | No            | Yes     |
| `/api/impression/{id}` | POST   | Log ad impression (view)                  | No            | Yes     |
| `/embed.js`            | GET    | JavaScript embed script                   | No            | Yes     |
| `/api/ads`             | GET    | List all ads                              | Yes           | Yes     |
| `/api/ad/add`          | POST   | Create new ad                             | Yes           | Yes     |
| `/api/ad/update/{id}`  | PUT    | Update existing ad                        | Yes           | Yes     |
| `/api/ad/delete/{id}`  | DELETE | Delete ad                                 | Yes           | Yes     |
| `/api/campaigns`       | GET    | List campaigns                            | Yes           | Yes     |
| `/api/campaign/add`    | POST   | Create new campaign                       | Yes           | Yes     |
| `/api/analytics/stats` | GET    | Get analytics statistics                  | Yes           | Yes     |
| `/api/upload`          | POST   | Upload image file                         | Yes           | Yes     |
| `/admin`               | GET    | Admin dashboard                           | Yes (in UI)   | Yes     |
| `/static/*`            | GET    | Static files                              | No            | Yes     |

## Embedding Ads on Your Website

### Simple Embed
```html
<div id="ad-container"></div>
<script src="http://localhost:8080/embed.js"></script>
```

### With Tag Filtering
```html
<div id="ad-container" 
     data-tags="tech,developer" 
     data-api-url="http://localhost:8080"></div>
<script src="http://localhost:8080/embed.js"></script>
```

### Custom Implementation
```html
<div id="ad-slot"></div>
<script>
(async () => {
  const tags = ['organic', 'fair-trade'];
  const url = `http://localhost:8080/api/ad/random?tags=${tags.join(',')}`;
  
  const res = await fetch(url);
  const ad = await res.json();
  
  const slot = document.getElementById('ad-slot');
  if (ad.ad_type === 'text') {
    slot.innerHTML = `<div>${ad.content}</div>`;
  } else if (ad.ad_type === 'image') {
    slot.innerHTML = `<img src="${ad.image_url}" />`;
  }
  
  // Log impression
  await fetch(`http://localhost:8080/api/impression/${ad.id}`, {
    method: 'POST'
  });
})();
</script>
```

## JSON Data Files

The server can preload data from JSON files on startup:

### campaigns.json
```json
[
  {
    "name": "Summer Sale",
    "created_at": "2025-10-01T12:00:00Z"
  }
]
```

### ads.json
```json
[
  {
    "campaign_id": 1,
    "ad_type": "text",
    "content": "Try our Go microframework today!",
    "redirect_url": "https://example.com/ad1",
    "tags": ["developer", "go", "backend"]
  },
  {
    "campaign_id": 1,
    "ad_type": "image",
    "image_url": "/static/images/banner.jpg",
    "redirect_url": "https://example.com/ad2",
    "tags": ["organic", "fair-trade"],
    "expires_at": "2025-12-31T23:59:59Z"
  }
]
```

### impressions.json
```json
[
  {
    "ad_id": 1,
    "action_type": "view",
    "ip": "127.0.0.1",
    "user_agent": "Mozilla/5.0...",
    "viewed_at": "2025-11-01T10:00:00Z"
  }
]
```

## Database Schema

The SQLite database has three tables:

- **campaigns**: Campaign information
- **ads**: Ad content and metadata
- **impressions**: View and click tracking

See `db_schema.sql` for the complete schema.

## Development

### Run in development mode with hot reload
```bash
cargo install cargo-watch
ADSERVER_API_TOKEN=dev cargo watch -x run
```

### Run tests
```bash
cargo test
```

### Build for production
```bash
cargo build --release
./target/release/taggy-adserver
```

## Environment Variables

- `ADSERVER_API_TOKEN` (required): Authentication token for protected endpoints
- `RUST_LOG` (optional): Logging level (e.g., `debug`, `info`, `warn`, `error`)

## Production Considerations

1. **API Token**: Use a strong, randomly generated token
2. **CORS**: Update `allowedOrigins` in the code for production domains
3. **HTTPS**: Run behind a reverse proxy (nginx, Caddy) with TLS
4. **Database**: Consider using a connection pool with appropriate limits
5. **File Uploads**: Add file size limits and validation
6. **Rate Limiting**: Implement rate limiting for public endpoints

## License

MIT
