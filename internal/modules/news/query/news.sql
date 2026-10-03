-- name: ListSources :many
SELECT * FROM news_sources ORDER BY name,id;
-- name: GetSource :one
SELECT * FROM news_sources WHERE id=?;
-- name: CountSources :one
SELECT COUNT(*) FROM news_sources;
-- name: SaveSource :exec
INSERT INTO news_sources(id,name,kind,url,query,topic,enabled,interval_minutes)
VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET
 name=excluded.name,kind=excluded.kind,url=excluded.url,query=excluded.query,topic=excluded.topic,
 enabled=excluded.enabled,interval_minutes=excluded.interval_minutes,
 etag=CASE WHEN news_sources.url=excluded.url THEN news_sources.etag ELSE '' END,
 last_modified=CASE WHEN news_sources.url=excluded.url THEN news_sources.last_modified ELSE '' END,
 next_fetch_at=0;
-- name: DeleteSource :execrows
DELETE FROM news_sources WHERE id=?;
-- name: RecordFetch :exec
UPDATE news_sources SET fetched_at=?,next_fetch_at=?,last_error='',failure_count=0,etag=?,last_modified=?,item_count=? WHERE id=?;
-- name: RecordFetchFailure :exec
UPDATE news_sources SET next_fetch_at=?,last_error=?,failure_count=failure_count+1 WHERE id=?;
-- name: ListRules :many
SELECT * FROM news_rules ORDER BY name,id;
-- name: SaveRule :exec
INSERT INTO news_rules(id,name,include_json,exclude_json,sources_json,topic,enabled)
VALUES(?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET name=excluded.name,include_json=excluded.include_json,
exclude_json=excluded.exclude_json,sources_json=excluded.sources_json,topic=excluded.topic,enabled=excluded.enabled;
-- name: GetRule :one
SELECT * FROM news_rules WHERE id=?;
-- name: DeleteRule :execrows
DELETE FROM news_rules WHERE id=?;
-- name: GetSettings :one
SELECT * FROM news_settings WHERE id=1;
-- name: SaveSettings :exec
UPDATE news_settings SET settings_json=? WHERE id=1;
-- name: SetDigestAttempt :exec
UPDATE news_settings SET last_digest_attempt_at=? WHERE id=1;
-- name: SetDigestError :exec
UPDATE news_settings SET last_digest_error=? WHERE id=1;
-- name: SetDigestSuccess :exec
UPDATE news_settings SET last_digest_at=?,last_digest_error='' WHERE id=1;
-- name: GetArticle :one
SELECT * FROM news_articles WHERE id=?;
-- name: GetArticleByURL :one
SELECT * FROM news_articles WHERE url=? ORDER BY created_at,id LIMIT 1;
-- name: MergeArticleState :exec
UPDATE news_articles SET is_read=CASE WHEN is_read=1 OR CAST(sqlc.arg(is_read) AS BIGINT)=1 THEN 1 ELSE 0 END,
starred=CASE WHEN starred=1 OR CAST(sqlc.arg(starred) AS BIGINT)=1 THEN 1 ELSE 0 END WHERE id=sqlc.arg(id);
-- name: MoveOrigins :exec
UPDATE news_origins SET article_id=sqlc.arg(target) WHERE article_id=sqlc.arg(previous);
-- name: DeleteArticle :exec
DELETE FROM news_articles WHERE id=?;
-- name: GetOriginArticle :one
SELECT article_id FROM news_origins WHERE source_id=? AND item_key=?;
-- name: InsertArticle :execrows
INSERT INTO news_articles(id,title,url,source_name,content,language,topic,matched,published_at,created_at)
VALUES(?,?,?,?,?,?,?,?,?,?) ON CONFLICT(id) DO NOTHING;
-- name: SaveOrigin :exec
INSERT INTO news_origins(source_id,item_key,article_id,source_name,url) VALUES(?,?,?,?,?)
ON CONFLICT(source_id,item_key) DO UPDATE SET source_name=excluded.source_name,url=excluded.url,article_id=excluded.article_id;
-- name: ListOrigins :many
SELECT DISTINCT source_id,source_name,url FROM news_origins WHERE article_id=? ORDER BY source_name,source_id,url;
-- name: UpdateArticleText :exec
UPDATE news_articles SET title=?,content=?,language=?,url=?,ai_status='none',ai_cache_key='',ai_error='',
translated_title='',translated_content='',summary='' WHERE id=?;
-- name: SetArticleMatch :exec
UPDATE news_articles SET topic=?,matched=? WHERE id=?;
-- name: SetArticleRead :exec
UPDATE news_articles SET is_read=? WHERE id=?;
-- name: SetArticleStarred :exec
UPDATE news_articles SET starred=? WHERE id=?;
-- name: ListArticlesPage :many
SELECT * FROM news_articles a
WHERE (CAST(sqlc.arg(search) AS TEXT)='' OR (LOWER(a.title) LIKE CAST(sqlc.arg(pattern) AS TEXT) ESCAPE '\')
 OR (LOWER(a.content) LIKE CAST(sqlc.arg(pattern) AS TEXT) ESCAPE '\')
 OR (LOWER(a.translated_title) LIKE CAST(sqlc.arg(pattern) AS TEXT) ESCAPE '\'))
AND (CAST(sqlc.arg(source_id) AS TEXT)='' OR EXISTS(SELECT 1 FROM news_origins o WHERE o.article_id=a.id AND o.source_id=sqlc.arg(source_id)))
AND (CAST(sqlc.arg(topic) AS TEXT)='' OR a.topic=sqlc.arg(topic))
AND (CAST(sqlc.arg(unread) AS BIGINT)=0 OR a.is_read=0)
AND (CAST(sqlc.arg(starred) AS BIGINT)=0 OR a.starred=1)
AND (CAST(sqlc.arg(matched) AS BIGINT)=0 OR a.matched=1)
AND (a.published_at < CAST(sqlc.arg(cursor_time) AS BIGINT) OR (a.published_at=CAST(sqlc.arg(cursor_time) AS BIGINT) AND a.id < CAST(sqlc.arg(cursor_id) AS TEXT)))
ORDER BY a.published_at DESC,a.id DESC LIMIT sqlc.arg(page_limit);
-- name: ListAllArticles :many
SELECT * FROM news_articles ORDER BY id;
-- name: ListAICandidates :many
SELECT id FROM news_articles WHERE matched=1 AND (ai_status='none' OR (ai_status IN('running','error') AND ai_attempted_at<?))
ORDER BY published_at DESC,id DESC LIMIT 3;
-- name: StartArticleAI :exec
UPDATE news_articles SET ai_status='running',ai_error='',ai_attempted_at=? WHERE id=?;
-- name: FinishArticleAI :exec
UPDATE news_articles SET translated_title=?,translated_content=?,summary=?,ai_status='ready',ai_error='',ai_cache_key=? WHERE id=?;
-- name: FailArticleAI :exec
UPDATE news_articles SET ai_status='error',ai_error=? WHERE id=?;
-- name: ResetArticleAI :exec
UPDATE news_articles SET ai_status='none',ai_cache_key='',translated_title='',translated_content='',summary='',ai_error='';
-- name: ReserveAI :one
INSERT INTO news_ai_usage(day,calls) VALUES(?,1)
ON CONFLICT(day) DO UPDATE SET calls=news_ai_usage.calls+1 RETURNING calls;
-- name: GetAIUsage :one
SELECT calls FROM news_ai_usage WHERE day=?;
-- name: RefundAI :exec
UPDATE news_ai_usage SET calls=calls-1 WHERE day=? AND calls>0;
-- name: NewsStats :one
SELECT COUNT(*) AS articles,
CAST(COALESCE(SUM(CASE WHEN is_read=0 THEN 1 ELSE 0 END),0) AS BIGINT) AS unread,
CAST(COALESCE(SUM(starred),0) AS BIGINT) AS starred FROM news_articles;
-- name: DeleteOldArticles :exec
DELETE FROM news_articles WHERE published_at<? AND starred=0;
-- name: DeleteOldDigests :exec
DELETE FROM news_digests WHERE created_at<?;
-- name: DeleteOldUsage :exec
DELETE FROM news_ai_usage WHERE day<?;
-- name: GetDigest :one
SELECT * FROM news_digests WHERE id=?;
-- name: GetDigestBySlot :one
SELECT * FROM news_digests WHERE slot=?;
-- name: InsertDigest :exec
INSERT INTO news_digests(id,slot,title,topic,content,items_json,created_at,ai_used,notification_id) VALUES(?,?,?,?,?,?,?,?,?);
-- name: SetDigestNotification :exec
UPDATE news_digests SET notification_id=? WHERE id=?;
-- name: ListDigestsPage :many
SELECT * FROM news_digests WHERE created_at<CAST(sqlc.arg(cursor_time) AS BIGINT)
OR (created_at=CAST(sqlc.arg(cursor_time) AS BIGINT) AND id<CAST(sqlc.arg(cursor_id) AS TEXT))
ORDER BY created_at DESC,id DESC LIMIT sqlc.arg(page_limit);
-- name: DigestArticles :many
SELECT * FROM news_articles WHERE matched=1 AND published_at>=CAST(sqlc.arg(since) AS BIGINT)
AND published_at<=CAST(sqlc.arg(until) AS BIGINT) AND (CAST(sqlc.arg(topic) AS TEXT)='' OR topic=sqlc.arg(topic))
ORDER BY published_at DESC,id DESC LIMIT sqlc.arg(item_limit);
