CREATE TABLE IF NOT EXISTS campaigns (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);
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
);
CREATE TABLE IF NOT EXISTS impressions (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    ad_id INTEGER NOT NULL,
    action_type TEXT NOT NULL CHECK(action_type IN ('view', 'click')),
    ip TEXT,
    user_agent TEXT,
    viewed_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (ad_id) REFERENCES ads(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_ads_expires ON ads(expires_at);
CREATE INDEX IF NOT EXISTS idx_impressions_ad ON impressions(ad_id, action_type);