-- +goose Up
CREATE TABLE event_consumer_topics (
    consumer_id TEXT NOT NULL,
    topic       TEXT NOT NULL,
    PRIMARY KEY (consumer_id, topic)
);

CREATE INDEX idx_event_consumer_topics_topic
ON event_consumer_topics(topic, consumer_id);

-- +goose Down
DROP TABLE event_consumer_topics;
