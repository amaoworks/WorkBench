-- +goose Up
ALTER TABLE news_sources RENAME TO news_sources_legacy;
ALTER TABLE news_articles RENAME TO news_articles_legacy;
ALTER TABLE news_origins RENAME TO news_origins_legacy;
ALTER TABLE news_rules RENAME TO news_rules_legacy;
ALTER TABLE news_settings RENAME TO news_settings_legacy;
ALTER TABLE news_digests RENAME TO news_digests_legacy;
ALTER TABLE news_ai_usage RENAME TO news_ai_usage_legacy;

CREATE TABLE news_sources (
    user_id TEXT NOT NULL CHECK(user_id<>''),
    id TEXT NOT NULL, name TEXT NOT NULL, kind TEXT NOT NULL,
    url TEXT NOT NULL, query TEXT NOT NULL DEFAULT '', topic TEXT NOT NULL DEFAULT '',
    enabled INTEGER NOT NULL DEFAULT 1 CHECK(enabled IN (0,1)),
    interval_minutes INTEGER NOT NULL DEFAULT 30,
    fetched_at BIGINT NOT NULL DEFAULT 0, next_fetch_at BIGINT NOT NULL DEFAULT 0,
    last_error TEXT NOT NULL DEFAULT '', failure_count INTEGER NOT NULL DEFAULT 0,
    etag TEXT NOT NULL DEFAULT '', last_modified TEXT NOT NULL DEFAULT '',
    item_count INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY(user_id,id)
);
CREATE TABLE news_articles (
    user_id TEXT NOT NULL CHECK(user_id<>''),
    id TEXT NOT NULL, title TEXT NOT NULL, url TEXT NOT NULL,
    source_name TEXT NOT NULL, content TEXT NOT NULL, language TEXT NOT NULL DEFAULT '',
    topic TEXT NOT NULL DEFAULT '', matched INTEGER NOT NULL DEFAULT 1 CHECK(matched IN(0,1)),
    is_read INTEGER NOT NULL DEFAULT 0 CHECK(is_read IN(0,1)),
    starred INTEGER NOT NULL DEFAULT 0 CHECK(starred IN(0,1)),
    published_at BIGINT NOT NULL, created_at BIGINT NOT NULL,
    translated_title TEXT NOT NULL DEFAULT '', translated_content TEXT NOT NULL DEFAULT '',
    summary TEXT NOT NULL DEFAULT '', ai_status TEXT NOT NULL DEFAULT 'none',
    ai_error TEXT NOT NULL DEFAULT '', ai_cache_key TEXT NOT NULL DEFAULT '',
    ai_attempted_at BIGINT NOT NULL DEFAULT 0,
    PRIMARY KEY(user_id,id)
);
CREATE TABLE news_origins (
    user_id TEXT NOT NULL CHECK(user_id<>''),
    source_id TEXT NOT NULL,
    item_key TEXT NOT NULL, article_id TEXT NOT NULL,
    source_name TEXT NOT NULL, url TEXT NOT NULL,
    PRIMARY KEY(user_id,source_id,item_key),
    FOREIGN KEY(user_id,source_id) REFERENCES news_sources(user_id,id) ON DELETE CASCADE,
    FOREIGN KEY(user_id,article_id) REFERENCES news_articles(user_id,id) ON DELETE CASCADE
);
CREATE TABLE news_rules (
    user_id TEXT NOT NULL CHECK(user_id<>''),
    id TEXT NOT NULL, name TEXT NOT NULL, include_json TEXT NOT NULL,
    exclude_json TEXT NOT NULL, sources_json TEXT NOT NULL, topic TEXT NOT NULL DEFAULT '',
    enabled INTEGER NOT NULL DEFAULT 1 CHECK(enabled IN(0,1)),
    PRIMARY KEY(user_id,id)
);
CREATE TABLE news_settings (
    user_id TEXT NOT NULL CHECK(user_id<>''),
    settings_json TEXT NOT NULL DEFAULT '{}',
    last_digest_at BIGINT NOT NULL DEFAULT 0, last_digest_error TEXT NOT NULL DEFAULT '',
    last_digest_attempt_at BIGINT NOT NULL DEFAULT 0,
    PRIMARY KEY(user_id)
);
CREATE TABLE news_digests (
    user_id TEXT NOT NULL CHECK(user_id<>''),
    id TEXT NOT NULL, slot TEXT NOT NULL, title TEXT NOT NULL, topic TEXT NOT NULL,
    content TEXT NOT NULL, items_json TEXT NOT NULL, created_at BIGINT NOT NULL,
    ai_used INTEGER NOT NULL CHECK(ai_used IN(0,1)), notification_id TEXT NOT NULL DEFAULT '',
    PRIMARY KEY(user_id,id),
    UNIQUE(user_id,slot)
);
CREATE TABLE news_ai_usage (
    user_id TEXT NOT NULL CHECK(user_id<>''),
    day TEXT NOT NULL, calls INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY(user_id,day)
);

INSERT INTO news_sources(user_id,id,name,kind,url,query,topic,enabled,interval_minutes,fetched_at,next_fetch_at,last_error,failure_count,etag,last_modified,item_count) SELECT 'owner',id,name,kind,url,query,topic,enabled,interval_minutes,fetched_at,next_fetch_at,last_error,failure_count,etag,last_modified,item_count FROM news_sources_legacy;
INSERT INTO news_articles(user_id,id,title,url,source_name,content,language,topic,matched,is_read,starred,published_at,created_at,translated_title,translated_content,summary,ai_status,ai_error,ai_cache_key,ai_attempted_at) SELECT 'owner',id,title,url,source_name,content,language,topic,matched,is_read,starred,published_at,created_at,translated_title,translated_content,summary,ai_status,ai_error,ai_cache_key,ai_attempted_at FROM news_articles_legacy;
INSERT INTO news_origins(user_id,source_id,item_key,article_id,source_name,url) SELECT 'owner',source_id,item_key,article_id,source_name,url FROM news_origins_legacy;
INSERT INTO news_rules(user_id,id,name,include_json,exclude_json,sources_json,topic,enabled) SELECT 'owner',id,name,include_json,exclude_json,sources_json,topic,enabled FROM news_rules_legacy;
INSERT INTO news_settings(user_id,settings_json,last_digest_at,last_digest_error,last_digest_attempt_at) SELECT 'owner',settings_json,last_digest_at,last_digest_error,last_digest_attempt_at FROM news_settings_legacy;
INSERT INTO news_digests(user_id,id,slot,title,topic,content,items_json,created_at,ai_used,notification_id) SELECT 'owner',id,slot,title,topic,content,items_json,created_at,ai_used,notification_id FROM news_digests_legacy;
INSERT INTO news_ai_usage(user_id,day,calls) SELECT 'owner',day,calls FROM news_ai_usage_legacy;
DROP TABLE news_origins_legacy;
DROP TABLE news_sources_legacy;
DROP TABLE news_articles_legacy;
DROP TABLE news_rules_legacy;
DROP TABLE news_settings_legacy;
DROP TABLE news_digests_legacy;
DROP TABLE news_ai_usage_legacy;
CREATE UNIQUE INDEX idx_news_articles_url ON news_articles(user_id,url) WHERE url<>'';
CREATE INDEX idx_news_articles_page ON news_articles(user_id,published_at DESC,id DESC);
CREATE INDEX idx_news_articles_ai ON news_articles(user_id,matched,ai_status,ai_attempted_at);
CREATE INDEX idx_news_origins_article ON news_origins(user_id,article_id);
CREATE INDEX idx_news_digests_page ON news_digests(user_id,created_at DESC,id DESC);

-- +goose Down
-- Rollback is permitted only before another user has private data.
CREATE TABLE news_rollback_guard (non_owner_count BIGINT CHECK(non_owner_count=0));
INSERT INTO news_rollback_guard(non_owner_count)
SELECT COUNT(*) FROM (
SELECT user_id FROM news_sources WHERE user_id<>'owner'
UNION ALL
SELECT user_id FROM news_articles WHERE user_id<>'owner'
UNION ALL
SELECT user_id FROM news_origins WHERE user_id<>'owner'
UNION ALL
SELECT user_id FROM news_rules WHERE user_id<>'owner'
UNION ALL
SELECT user_id FROM news_settings WHERE user_id<>'owner'
UNION ALL
SELECT user_id FROM news_digests WHERE user_id<>'owner'
UNION ALL
SELECT user_id FROM news_ai_usage WHERE user_id<>'owner'
) AS private_users;
DROP TABLE news_rollback_guard;
ALTER TABLE news_sources RENAME TO news_sources_owned;
ALTER TABLE news_articles RENAME TO news_articles_owned;
ALTER TABLE news_origins RENAME TO news_origins_owned;
ALTER TABLE news_rules RENAME TO news_rules_owned;
ALTER TABLE news_settings RENAME TO news_settings_owned;
ALTER TABLE news_digests RENAME TO news_digests_owned;
ALTER TABLE news_ai_usage RENAME TO news_ai_usage_owned;

CREATE TABLE news_sources (
    id TEXT PRIMARY KEY, name TEXT NOT NULL, kind TEXT NOT NULL,
    url TEXT NOT NULL, query TEXT NOT NULL DEFAULT '', topic TEXT NOT NULL DEFAULT '',
    enabled INTEGER NOT NULL DEFAULT 1 CHECK(enabled IN (0,1)),
    interval_minutes INTEGER NOT NULL DEFAULT 30,
    fetched_at BIGINT NOT NULL DEFAULT 0, next_fetch_at BIGINT NOT NULL DEFAULT 0,
    last_error TEXT NOT NULL DEFAULT '', failure_count INTEGER NOT NULL DEFAULT 0,
    etag TEXT NOT NULL DEFAULT '', last_modified TEXT NOT NULL DEFAULT '',
    item_count INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE news_articles (
    id TEXT PRIMARY KEY, title TEXT NOT NULL, url TEXT NOT NULL,
    source_name TEXT NOT NULL, content TEXT NOT NULL, language TEXT NOT NULL DEFAULT '',
    topic TEXT NOT NULL DEFAULT '', matched INTEGER NOT NULL DEFAULT 1 CHECK(matched IN(0,1)),
    is_read INTEGER NOT NULL DEFAULT 0 CHECK(is_read IN(0,1)),
    starred INTEGER NOT NULL DEFAULT 0 CHECK(starred IN(0,1)),
    published_at BIGINT NOT NULL, created_at BIGINT NOT NULL,
    translated_title TEXT NOT NULL DEFAULT '', translated_content TEXT NOT NULL DEFAULT '',
    summary TEXT NOT NULL DEFAULT '', ai_status TEXT NOT NULL DEFAULT 'none',
    ai_error TEXT NOT NULL DEFAULT '', ai_cache_key TEXT NOT NULL DEFAULT '',
    ai_attempted_at BIGINT NOT NULL DEFAULT 0
);
CREATE TABLE news_origins (
    source_id TEXT NOT NULL REFERENCES news_sources(id) ON DELETE CASCADE,
    item_key TEXT NOT NULL, article_id TEXT NOT NULL REFERENCES news_articles(id) ON DELETE CASCADE,
    source_name TEXT NOT NULL, url TEXT NOT NULL,
    PRIMARY KEY(source_id,item_key)
);
CREATE TABLE news_rules (
    id TEXT PRIMARY KEY, name TEXT NOT NULL, include_json TEXT NOT NULL,
    exclude_json TEXT NOT NULL, sources_json TEXT NOT NULL, topic TEXT NOT NULL DEFAULT '',
    enabled INTEGER NOT NULL DEFAULT 1 CHECK(enabled IN(0,1))
);
CREATE TABLE news_settings (
    id INTEGER PRIMARY KEY CHECK(id=1), settings_json TEXT NOT NULL DEFAULT '{}',
    last_digest_at BIGINT NOT NULL DEFAULT 0, last_digest_error TEXT NOT NULL DEFAULT '',
    last_digest_attempt_at BIGINT NOT NULL DEFAULT 0
);
CREATE TABLE news_digests (
    id TEXT PRIMARY KEY, slot TEXT NOT NULL UNIQUE, title TEXT NOT NULL, topic TEXT NOT NULL,
    content TEXT NOT NULL, items_json TEXT NOT NULL, created_at BIGINT NOT NULL,
    ai_used INTEGER NOT NULL CHECK(ai_used IN(0,1)), notification_id TEXT NOT NULL DEFAULT ''
);
CREATE TABLE news_ai_usage (day TEXT PRIMARY KEY, calls INTEGER NOT NULL DEFAULT 0);
INSERT INTO news_sources(id,name,kind,url,query,topic,enabled,interval_minutes,fetched_at,next_fetch_at,last_error,failure_count,etag,last_modified,item_count) SELECT id,name,kind,url,query,topic,enabled,interval_minutes,fetched_at,next_fetch_at,last_error,failure_count,etag,last_modified,item_count FROM news_sources_owned;
INSERT INTO news_articles(id,title,url,source_name,content,language,topic,matched,is_read,starred,published_at,created_at,translated_title,translated_content,summary,ai_status,ai_error,ai_cache_key,ai_attempted_at) SELECT id,title,url,source_name,content,language,topic,matched,is_read,starred,published_at,created_at,translated_title,translated_content,summary,ai_status,ai_error,ai_cache_key,ai_attempted_at FROM news_articles_owned;
INSERT INTO news_origins(source_id,item_key,article_id,source_name,url) SELECT source_id,item_key,article_id,source_name,url FROM news_origins_owned;
INSERT INTO news_rules(id,name,include_json,exclude_json,sources_json,topic,enabled) SELECT id,name,include_json,exclude_json,sources_json,topic,enabled FROM news_rules_owned;
INSERT INTO news_settings(id,settings_json,last_digest_at,last_digest_error,last_digest_attempt_at) SELECT 1,settings_json,last_digest_at,last_digest_error,last_digest_attempt_at FROM news_settings_owned;
INSERT INTO news_digests(id,slot,title,topic,content,items_json,created_at,ai_used,notification_id) SELECT id,slot,title,topic,content,items_json,created_at,ai_used,notification_id FROM news_digests_owned;
INSERT INTO news_ai_usage(day,calls) SELECT day,calls FROM news_ai_usage_owned;
DROP TABLE news_origins_owned;
DROP TABLE news_sources_owned;
DROP TABLE news_articles_owned;
DROP TABLE news_rules_owned;
DROP TABLE news_settings_owned;
DROP TABLE news_digests_owned;
DROP TABLE news_ai_usage_owned;
CREATE INDEX idx_news_articles_page ON news_articles(published_at DESC,id DESC);
CREATE UNIQUE INDEX idx_news_articles_url ON news_articles(url) WHERE url<>'';
CREATE INDEX idx_news_articles_ai ON news_articles(matched,ai_status,ai_attempted_at);
CREATE INDEX idx_news_origins_article ON news_origins(article_id);
CREATE INDEX idx_news_digests_page ON news_digests(created_at DESC,id DESC);
