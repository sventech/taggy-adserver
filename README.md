# taggy adserver

Simple, privacy-first advertising server with targeting based on explicit user preferences.

## Usage

Run development server
```bash
ADSERVER_API_TOKEN=mysecret \
go run main.go
```

Add a new advert (image-based)
```bash
curl -X POST http://localhost:8080/api/ad/add \
  -H "Authorization: Bearer mysecret" \
  -H "Content-Type: application/json" \
  -d '{"campaign_id":1,"ad_type":"image","image_url":"https://cdn.example.com/ads/banner.jpg","redirect_url":"https://example.com/landing","tags":["organic","fair-trade"],"expires_at":"2025-12-31T23:59:59Z"}'
```
Example output: `{"id":"8075167432580373727","status":"ok"}`

Get a random advert
```bash
curl http://localhost:8080/api/ad/random
```

Text ad example:
`{"id":"2","ad_type":"text","content":"Super fast and lightweight backend in Go!"}`

Image ad example:
`{"id":"3","ad_type":"image","image_url":"/ads/image1.jpg"}`

Get an ad for organic or fair-trade preferences:
`curl "http://localhost:8080/api/ad/random?preferences=organic,fair-trade,patriotic"`

Example output:
```json
{
  "id": "3",
  "ad_type": "text",
  "content": "Ethically sourced clothing for modern humans.",
  "tags": ["organic", "fair-trade", "woman-owned"]
}
```

Images in CDN
```bash
curl -X POST http://localhost:8080/api/ad/add \
     -H "Content-Type: application/json" \
     -d '{"ad_type":"image","image_url":"https://cdn.example.com/ads/newbanner.png","tags":["vegan","organic"]}'
```

## authz / CORS
| Endpoint            | Method | Description                               | Auth             | CORS          |
| --------------------| ------ | ----------------------------------------- | ---------------- | ------------- |
| `/api/ad/random`    | GET    | Returns a random (optionally targeted) ad | ❌ No             | ✅ Restricted |
| `/api/redirect`     | GET    | Get the redirect link for an ad           | ❌ No             | ✅ Restricted |
| `/embed.js`         | GET    | Get the embed file for using ads          | ❌ No             | ✅ Restricted |
| `/api/ads`          | GET    | List current ads                          | ✅ Token required | ❌ No         |
| `/api/ad/add`       | POST   | Create a new ad                           | ✅ Token required | ❌ No         |
| `/api/ad/delete`    | POST   | Delete an ad                              | ✅ Token required | ❌ No         |
| `/api/ad/update`    | POST   | Update an ad                              | ✅ Token required | ❌ No         |
| `/api/impression`   | POST   | Register an impression (click/view)       | ❌ No             | ✅ Restricted |
| `/api/campaigns`    | GET    | List current campaigns                    | ✅ Token required | ✅ Restricted |
| `/api/campaign/add` | POST   | Create a new campaign                     | ✅ Token required | ✅ Restricted |
| `/api/analytics/stats` | GET | Get analytics about the current ads       | ✅ Token required | ✅ Restricted |
| `/admin`            | -      | Admin; manage ads &amp; campaigns         | ✅ Token required | ✅ Restricted |
| `/api/upload`       | POST   | Upload a file (generally an image)        | ✅ Token required | ✅ Restricted |

## Usage

Example usage:
```html
<div id="ad-slot"></div>
<script>
(async () => {
  try {
    const prefs = ['organic', 'fair-trade']; // customize
    const url = 'https://your-adserver.com/api/ad/random?preferences=' + prefs.join(',');
    const res = await fetch(url);
    if (!res.ok) throw new Error('Ad fetch failed');
    const ad = await res.json();

    const slot = document.getElementById('ad-slot');
    if (ad.type === 'text') {
      slot.innerHTML = `<div style="padding:10px;background:#f8f8f8;border-radius:6px;">${ad.content}</div>`;
    } else if (ad.type === 'image') {
      slot.innerHTML = `<a href="#"><img src="${ad.image_url}" style="max-width:100%;border-radius:6px;"/></a>`;
    }
  } catch (err) {
    console.error('Ad load error:', err);
  }
})();
</script>
```

Example click / redirect:
`curl -v "http://localhost:8080/api/impression/2"`
