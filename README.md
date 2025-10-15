# taggy adserver

Simple, privacy-preserving ads server with targeting based on explicit user preferences

## Usage

Run development server
`go run main.go`

Add a new advert (image-based)
```bash
curl -X POST http://localhost:8080/api/ad/add \
     -H "Content-Type: application/json" \
     -d '{"type":"image","image_url":"https://cdn.example.com/ads/newbanner.png","tags":["vegan","organic"]}'
```
Example output: `{"id":"8075167432580373727","status":"ok"}`

Get a random advert
```bash
curl http://localhost:8080/api/ad/random
```

Text ad example:
`{"id":"2","type":"text","content":"Super fast and lightweight backend in Go!"}`

Image ad example:
`{"id":"3","type":"image","image_url":"/ads/image1.jpg"}`

Get an ad for organic or fair-trade preferences:
`curl "http://localhost:8080/api/ad/random?preferences=organic,fair-trade"`

Example output:
```json
{
  "id": "3",
  "type": "text",
  "content": "Ethically sourced clothing for modern humans.",
  "tags": ["organic", "fair-trade", "woman-owned"]
}
```

Images in CDN
```bash
curl -X POST http://localhost:8080/api/ad/add \
     -H "Content-Type: application/json" \
     -d '{"type":"image","image_url":"https://cdn.example.com/ads/newbanner.png","tags":["vegan","organic"]}'
```

## authz / CORS
| Endpoint         | Method | Description                               | Auth             | CORS         |
| ---------------- | ------ | ----------------------------------------- | ---------------- | ------------ |
| `/api/ad/random` | GET    | Returns a random (optionally targeted) ad | ❌ No             | ✅ Restricted |
| `/api/ad/add`    | POST   | Add a new ad                              | ✅ Token required | ❌ No         |
| `/api/ad/reload` | GET    | Reload ads from disk                      | ✅ Token required | ✅ Restricted |


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