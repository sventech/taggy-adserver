# truthiest adserver

Example ads server to explore how to build an efficient system.

## Usage

Add a new advert
```bash
curl -X POST http://localhost:8080/api/ad/add \
     -H "Content-Type: application/json" \
     -d '{"type": "image", "image_url": "image1.png"}'
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

