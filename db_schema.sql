CREATE TABLE IF NOT EXISTS campaigns(id INTEGER PRIMARY KEY, name TEXT);
CREATE TABLE IF NOT EXISTS ads(id INTEGER PRIMARY KEY, type TEXT, content TEXT, image_url TEXT, redirect_url TEXT, preferences TEXT, campaign_id INTEGER, expires_at DATETIME);
CREATE TABLE IF NOT EXISTS impressions(id INTEGER PRIMARY KEY, ad_id INTEGER, ip TEXT, user_agent TEXT, timestamp DATETIME);