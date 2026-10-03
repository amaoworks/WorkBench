-- +goose Up
CREATE TABLE news_sources (
    id TEXT PRIMARY KEY, name TEXT NOT NULL, kind TEXT NOT NULL,
    url TEXT NOT NULL, query TEXT NOT NULL DEFAULT '', topic TEXT NOT NULL DEFAULT '',
    enabled BIGINT NOT NULL DEFAULT 1 CHECK(enabled IN (0,1)),
    interval_minutes BIGINT NOT NULL DEFAULT 30,
    fetched_at BIGINT NOT NULL DEFAULT 0, next_fetch_at BIGINT NOT NULL DEFAULT 0,
    last_error TEXT NOT NULL DEFAULT '', failure_count BIGINT NOT NULL DEFAULT 0,
    etag TEXT NOT NULL DEFAULT '', last_modified TEXT NOT NULL DEFAULT '',
    item_count BIGINT NOT NULL DEFAULT 0
);
CREATE TABLE news_articles (
    id TEXT PRIMARY KEY, title TEXT NOT NULL, url TEXT NOT NULL,
    source_name TEXT NOT NULL, content TEXT NOT NULL, language TEXT NOT NULL DEFAULT '',
    topic TEXT NOT NULL DEFAULT '', matched BIGINT NOT NULL DEFAULT 1 CHECK(matched IN(0,1)),
    is_read BIGINT NOT NULL DEFAULT 0 CHECK(is_read IN(0,1)),
    starred BIGINT NOT NULL DEFAULT 0 CHECK(starred IN(0,1)),
    published_at BIGINT NOT NULL, created_at BIGINT NOT NULL,
    translated_title TEXT NOT NULL DEFAULT '', translated_content TEXT NOT NULL DEFAULT '',
    summary TEXT NOT NULL DEFAULT '', ai_status TEXT NOT NULL DEFAULT 'none',
    ai_error TEXT NOT NULL DEFAULT '', ai_cache_key TEXT NOT NULL DEFAULT '',
    ai_attempted_at BIGINT NOT NULL DEFAULT 0
);
CREATE INDEX idx_news_articles_page ON news_articles(published_at DESC,id DESC);
CREATE UNIQUE INDEX idx_news_articles_url ON news_articles(url) WHERE url<>'';
CREATE INDEX idx_news_articles_ai ON news_articles(matched,ai_status,ai_attempted_at);
CREATE TABLE news_origins (
    source_id TEXT NOT NULL REFERENCES news_sources(id) ON DELETE CASCADE,
    item_key TEXT NOT NULL, article_id TEXT NOT NULL REFERENCES news_articles(id) ON DELETE CASCADE,
    source_name TEXT NOT NULL, url TEXT NOT NULL,
    PRIMARY KEY(source_id,item_key)
);
CREATE INDEX idx_news_origins_article ON news_origins(article_id);
CREATE TABLE news_rules (
    id TEXT PRIMARY KEY, name TEXT NOT NULL, include_json TEXT NOT NULL,
    exclude_json TEXT NOT NULL, sources_json TEXT NOT NULL, topic TEXT NOT NULL DEFAULT '',
    enabled BIGINT NOT NULL DEFAULT 1 CHECK(enabled IN(0,1))
);
CREATE TABLE news_settings (
    id BIGINT PRIMARY KEY CHECK(id=1), settings_json TEXT NOT NULL DEFAULT '{}',
    last_digest_at BIGINT NOT NULL DEFAULT 0, last_digest_error TEXT NOT NULL DEFAULT '',
    last_digest_attempt_at BIGINT NOT NULL DEFAULT 0
);
INSERT INTO news_settings(id) VALUES(1);
CREATE TABLE news_digests (
    id TEXT PRIMARY KEY, slot TEXT NOT NULL UNIQUE, title TEXT NOT NULL, topic TEXT NOT NULL,
    content TEXT NOT NULL, items_json TEXT NOT NULL, created_at BIGINT NOT NULL,
    ai_used BIGINT NOT NULL CHECK(ai_used IN(0,1)), notification_id TEXT NOT NULL DEFAULT ''
);
CREATE INDEX idx_news_digests_page ON news_digests(created_at DESC,id DESC);
CREATE TABLE news_ai_usage (day TEXT PRIMARY KEY, calls BIGINT NOT NULL DEFAULT 0);

-- +goose Down
DROP TABLE news_ai_usage;
DROP TABLE news_digests;
DROP TABLE news_settings;
DROP TABLE news_rules;
DROP TABLE news_origins;
DROP TABLE news_articles;
DROP TABLE news_sources;
