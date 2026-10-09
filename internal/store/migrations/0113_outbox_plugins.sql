-- Plugins subscribe to the same events as webhooks and are delivered to through the
-- same outbox: a row for a plugin has hook_id 0 and names the plugin here. Every
-- subscriber of an event is still stored in one insert.
ALTER TABLE webhook_outbox ADD COLUMN plugin_id TEXT NOT NULL DEFAULT '';
