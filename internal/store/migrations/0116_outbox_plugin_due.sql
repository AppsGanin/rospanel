-- Each kind of delivery is found by its own index. The dispatcher asks for the
-- oldest due row of each plugin, and the endpoints' dispatcher for its due rows;
-- with a big plugin backlog (a bulk event for tens of thousands of users) neither
-- may mean reading past the other's rows on the single writer.
CREATE INDEX IF NOT EXISTS idx_outbox_plugin_due ON webhook_outbox(plugin_id, next_at, id) WHERE plugin_id <> '';
CREATE INDEX IF NOT EXISTS idx_outbox_endpoint_due ON webhook_outbox(next_at, id) WHERE plugin_id = '';
