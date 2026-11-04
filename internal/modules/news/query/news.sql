-- name: ListSources :many
SELECT * FROM news_sources WHERE user_id=sqlc.arg(user_id) ORDER BY name,id;
-- name: GetSource :one
SELECT * FROM news_sources WHERE user_id=sqlc.arg(user_id) AND id=sqlc.arg(id);
-- name: CountSources :one
SELECT COUNT(*) FROM news_sources WHERE user_id=sqlc.arg(user_id);
-- name: SaveSource :exec
INSERT INTO news_sources(user_id,id,name,kind,url,query,topic,enabled,interval_minutes)
VALUES(sqlc.arg(user_id),sqlc.arg(id),sqlc.arg(name),sqlc.arg(kind),sqlc.arg(url),sqlc.arg(query),sqlc.arg(topic),sqlc.arg(enabled),sqlc.arg(interval_minutes)) ON CONFLICT(user_id,id) DO UPDATE SET
 name=excluded.name,kind=excluded.kind,url=excluded.url,query=excluded.query,topic=excluded.topic,
 enabled=excluded.enabled,interval_minutes=excluded.interval_minutes,
 etag=CASE WHEN news_sources.url=excluded.url THEN news_sources.etag ELSE '' END,
 last_modified=CASE WHEN news_sources.url=excluded.url THEN news_sources.last_modified ELSE '' END,
 next_fetch_at=0;
-- name: DeleteSource :execrows
DELETE FROM news_sources WHERE user_id=sqlc.arg(user_id) AND id=sqlc.arg(id);
-- name: RecordFetch :exec
UPDATE news_sources SET fetched_at=sqlc.arg(fetched_at),next_fetch_at=sqlc.arg(next_fetch_at),last_error='',failure_count=0,etag=sqlc.arg(etag),last_modified=sqlc.arg(last_modified),item_count=sqlc.arg(item_count) WHERE user_id=sqlc.arg(user_id) AND id=sqlc.arg(id);
-- name: RecordFetchFailure :exec
UPDATE news_sources SET next_fetch_at=sqlc.arg(next_fetch_at),last_error=sqlc.arg(last_error),failure_count=failure_count+1 WHERE user_id=sqlc.arg(user_id) AND id=sqlc.arg(id);
-- name: ListRules :many
SELECT * FROM news_rules WHERE user_id=sqlc.arg(user_id) ORDER BY name,id;
-- name: SaveRule :exec
INSERT INTO news_rules(user_id,id,name,include_json,exclude_json,sources_json,topic,enabled)
VALUES(sqlc.arg(user_id),sqlc.arg(id),sqlc.arg(name),sqlc.arg(include_json),sqlc.arg(exclude_json),sqlc.arg(sources_json),sqlc.arg(topic),sqlc.arg(enabled)) ON CONFLICT(user_id,id) DO UPDATE SET name=excluded.name,include_json=excluded.include_json,
exclude_json=excluded.exclude_json,sources_json=excluded.sources_json,topic=excluded.topic,enabled=excluded.enabled;
-- name: GetRule :one
SELECT * FROM news_rules WHERE user_id=sqlc.arg(user_id) AND id=sqlc.arg(id);
-- name: DeleteRule :execrows
DELETE FROM news_rules WHERE user_id=sqlc.arg(user_id) AND id=sqlc.arg(id);
-- name: EnsureSettings :exec
INSERT INTO news_settings(user_id) VALUES(sqlc.arg(user_id)) ON CONFLICT(user_id) DO NOTHING;
-- name: GetSettings :one
SELECT * FROM news_settings WHERE user_id=sqlc.arg(user_id);
-- name: SaveSettings :exec
UPDATE news_settings SET settings_json=sqlc.arg(settings_json) WHERE user_id=sqlc.arg(user_id);
-- name: SetDigestAttempt :exec
UPDATE news_settings SET last_digest_attempt_at=sqlc.arg(last_digest_attempt_at) WHERE user_id=sqlc.arg(user_id);
-- name: SetDigestError :exec
UPDATE news_settings SET last_digest_error=sqlc.arg(last_digest_error) WHERE user_id=sqlc.arg(user_id);
-- name: SetDigestSuccess :exec
UPDATE news_settings SET last_digest_at=sqlc.arg(last_digest_at),last_digest_error='' WHERE user_id=sqlc.arg(user_id);
-- name: GetArticle :one
SELECT * FROM news_articles WHERE user_id=sqlc.arg(user_id) AND id=sqlc.arg(id);
-- name: GetArticleByURL :one
SELECT * FROM news_articles WHERE user_id=sqlc.arg(user_id) AND url=sqlc.arg(url) ORDER BY created_at,id LIMIT 1;
-- name: MergeArticleState :exec
UPDATE news_articles SET is_read=CASE WHEN is_read=1 OR CAST(sqlc.arg(is_read) AS BIGINT)=1 THEN 1 ELSE 0 END,
starred=CASE WHEN starred=1 OR CAST(sqlc.arg(starred) AS BIGINT)=1 THEN 1 ELSE 0 END WHERE user_id=sqlc.arg(user_id) AND id=sqlc.arg(id);
-- name: MoveOrigins :exec
UPDATE news_origins SET article_id=sqlc.arg(target) WHERE user_id=sqlc.arg(user_id) AND article_id=sqlc.arg(previous);
-- name: DeleteArticle :exec
DELETE FROM news_articles WHERE user_id=sqlc.arg(user_id) AND id=sqlc.arg(id);
-- name: GetOriginArticle :one
SELECT article_id FROM news_origins WHERE user_id=sqlc.arg(user_id) AND source_id=sqlc.arg(source_id) AND item_key=sqlc.arg(item_key);
-- name: InsertArticle :execrows
INSERT INTO news_articles(user_id,id,title,url,source_name,content,language,topic,matched,published_at,created_at)
VALUES(sqlc.arg(user_id),sqlc.arg(id),sqlc.arg(title),sqlc.arg(url),sqlc.arg(source_name),sqlc.arg(content),sqlc.arg(language),sqlc.arg(topic),sqlc.arg(matched),sqlc.arg(published_at),sqlc.arg(created_at)) ON CONFLICT(user_id,id) DO NOTHING;
-- name: SaveOrigin :exec
INSERT INTO news_origins(user_id,source_id,item_key,article_id,source_name,url) VALUES(sqlc.arg(user_id),sqlc.arg(source_id),sqlc.arg(item_key),sqlc.arg(article_id),sqlc.arg(source_name),sqlc.arg(url))
ON CONFLICT(user_id,source_id,item_key) DO UPDATE SET source_name=excluded.source_name,url=excluded.url,article_id=excluded.article_id;
-- name: ListOrigins :many
SELECT DISTINCT source_id,source_name,url FROM news_origins WHERE user_id=sqlc.arg(user_id) AND article_id=sqlc.arg(article_id) ORDER BY source_name,source_id,url;
-- name: UpdateArticleText :exec
UPDATE news_articles SET title=sqlc.arg(title),content=sqlc.arg(content),language=sqlc.arg(language),url=sqlc.arg(url),ai_status='none',ai_cache_key='',ai_error='',
translated_title='',translated_content='',summary='' WHERE user_id=sqlc.arg(user_id) AND id=sqlc.arg(id);
-- name: SetArticleMatch :exec
UPDATE news_articles SET topic=sqlc.arg(topic),matched=sqlc.arg(matched) WHERE user_id=sqlc.arg(user_id) AND id=sqlc.arg(id);
-- name: SetArticleRead :exec
UPDATE news_articles SET is_read=sqlc.arg(is_read) WHERE user_id=sqlc.arg(user_id) AND id=sqlc.arg(id);
-- name: SetArticleStarred :exec
UPDATE news_articles SET starred=sqlc.arg(starred) WHERE user_id=sqlc.arg(user_id) AND id=sqlc.arg(id);
-- name: ListArticlesPage :many
SELECT * FROM news_articles a
WHERE a.user_id=sqlc.arg(user_id)
AND (CAST(sqlc.arg(search) AS TEXT)='' OR (LOWER(a.title) LIKE CAST(sqlc.arg(pattern) AS TEXT) ESCAPE '\')
 OR (LOWER(a.content) LIKE CAST(sqlc.arg(pattern) AS TEXT) ESCAPE '\')
 OR (LOWER(a.translated_title) LIKE CAST(sqlc.arg(pattern) AS TEXT) ESCAPE '\'))
AND (CAST(sqlc.arg(source_id) AS TEXT)='' OR EXISTS(SELECT 1 FROM news_origins o WHERE o.user_id=a.user_id AND o.article_id=a.id AND o.source_id=sqlc.arg(source_id)))
AND (CAST(sqlc.arg(topic) AS TEXT)='' OR a.topic=sqlc.arg(topic))
AND (CAST(sqlc.arg(unread) AS BIGINT)=0 OR a.is_read=0)
AND (CAST(sqlc.arg(starred) AS BIGINT)=0 OR a.starred=1)
AND (CAST(sqlc.arg(matched) AS BIGINT)=0 OR a.matched=1)
AND (a.published_at < CAST(sqlc.arg(cursor_time) AS BIGINT) OR (a.published_at=CAST(sqlc.arg(cursor_time) AS BIGINT) AND a.id < CAST(sqlc.arg(cursor_id) AS TEXT)))
ORDER BY a.published_at DESC,a.id DESC LIMIT sqlc.arg(page_limit);
-- name: ListAllArticles :many
SELECT * FROM news_articles WHERE user_id=sqlc.arg(user_id) ORDER BY id;
-- name: ListAICandidates :many
SELECT id FROM news_articles WHERE user_id=sqlc.arg(user_id) AND matched=1 AND (ai_status='none' OR (ai_status IN('running','error') AND ai_attempted_at<sqlc.arg(ai_attempted_at)))
ORDER BY published_at DESC,id DESC LIMIT 3;
-- name: StartArticleAI :exec
UPDATE news_articles SET ai_status='running',ai_error='',ai_attempted_at=sqlc.arg(ai_attempted_at) WHERE user_id=sqlc.arg(user_id) AND id=sqlc.arg(id);
-- name: FinishArticleAI :exec
UPDATE news_articles SET translated_title=sqlc.arg(translated_title),translated_content=sqlc.arg(translated_content),summary=sqlc.arg(summary),ai_status='ready',ai_error='',ai_cache_key=sqlc.arg(ai_cache_key) WHERE user_id=sqlc.arg(user_id) AND id=sqlc.arg(id);
-- name: FailArticleAI :exec
UPDATE news_articles SET ai_status='error',ai_error=sqlc.arg(ai_error) WHERE user_id=sqlc.arg(user_id) AND id=sqlc.arg(id);
-- name: ResetArticleAI :exec
UPDATE news_articles SET ai_status='none',ai_cache_key='',translated_title='',translated_content='',summary='',ai_error='' WHERE user_id=sqlc.arg(user_id);
-- name: ReserveAI :one
INSERT INTO news_ai_usage(user_id,day,calls) VALUES(sqlc.arg(user_id),sqlc.arg(day),1)
ON CONFLICT(user_id,day) DO UPDATE SET calls=news_ai_usage.calls+1 RETURNING calls;
-- name: GetAIUsage :one
SELECT calls FROM news_ai_usage WHERE user_id=sqlc.arg(user_id) AND day=sqlc.arg(day);
-- name: RefundAI :exec
UPDATE news_ai_usage SET calls=calls-1 WHERE user_id=sqlc.arg(user_id) AND day=sqlc.arg(day) AND calls>0;
-- name: NewsStats :one
SELECT COUNT(*) AS articles,
CAST(COALESCE(SUM(CASE WHEN is_read=0 THEN 1 ELSE 0 END),0) AS BIGINT) AS unread,
CAST(COALESCE(SUM(starred),0) AS BIGINT) AS starred FROM news_articles WHERE user_id=sqlc.arg(user_id);
-- name: DeleteOldArticles :exec
DELETE FROM news_articles WHERE user_id=sqlc.arg(user_id) AND published_at<sqlc.arg(published_at) AND starred=0;
-- name: DeleteOldDigests :exec
DELETE FROM news_digests WHERE user_id=sqlc.arg(user_id) AND created_at<sqlc.arg(created_at);
-- name: DeleteOldUsage :exec
DELETE FROM news_ai_usage WHERE user_id=sqlc.arg(user_id) AND day<sqlc.arg(day);
-- name: GetDigest :one
SELECT * FROM news_digests WHERE user_id=sqlc.arg(user_id) AND id=sqlc.arg(id);
-- name: GetDigestBySlot :one
SELECT * FROM news_digests WHERE user_id=sqlc.arg(user_id) AND slot=sqlc.arg(slot);
-- name: InsertDigest :exec
INSERT INTO news_digests(user_id,id,slot,title,topic,content,items_json,created_at,ai_used,notification_id) VALUES(sqlc.arg(user_id),sqlc.arg(id),sqlc.arg(slot),sqlc.arg(title),sqlc.arg(topic),sqlc.arg(content),sqlc.arg(items_json),sqlc.arg(created_at),sqlc.arg(ai_used),sqlc.arg(notification_id));
-- name: SetDigestNotification :exec
UPDATE news_digests SET notification_id=sqlc.arg(notification_id) WHERE user_id=sqlc.arg(user_id) AND id=sqlc.arg(id);
-- name: ListDigestsPage :many
SELECT * FROM news_digests WHERE user_id=sqlc.arg(user_id) AND (created_at<CAST(sqlc.arg(cursor_time) AS BIGINT)
OR (created_at=CAST(sqlc.arg(cursor_time) AS BIGINT) AND id<CAST(sqlc.arg(cursor_id) AS TEXT)))
ORDER BY created_at DESC,id DESC LIMIT sqlc.arg(page_limit);
-- name: DigestArticles :many
SELECT * FROM news_articles WHERE user_id=sqlc.arg(user_id) AND matched=1 AND published_at>=CAST(sqlc.arg(since) AS BIGINT)
AND published_at<=CAST(sqlc.arg(until) AS BIGINT) AND (CAST(sqlc.arg(topic) AS TEXT)='' OR topic=sqlc.arg(topic))
ORDER BY published_at DESC,id DESC LIMIT sqlc.arg(item_limit);
