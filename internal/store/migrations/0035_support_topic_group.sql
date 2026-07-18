-- Scope every topic mapping to the group that issued it.
--
-- A thread id is a message id, and message ids are only unique WITHIN a chat. The
-- mapping table keyed them globally, so mappings made in group A stayed addressable
-- after support was pointed at group B: an admin writing in B's topic 7 had it
-- delivered to whoever owned topic 7 in A, and that user's next message was forwarded
-- into a stranger's thread.
--
-- Guarding that with "reset the mappings when the group changes" turned out to be the
-- wrong shape — it has to be exactly right on every path (A→B, A→0→B, re-selecting A
-- after clearing the field), and getting any one of them wrong either leaks messages
-- across customers or silently orphans live conversations that Telegram gives no way
-- to find again. Carrying the group on the row makes the question local: a mapping
-- matches only inside the group it came from, and no transition needs handling.
ALTER TABLE tg_support_topics ADD COLUMN group_id INTEGER NOT NULL DEFAULT 0;

-- Existing rows were all issued by the currently configured group.
UPDATE tg_support_topics
   SET group_id = COALESCE((SELECT tg_support_group_id FROM settings WHERE id = 1), 0);

-- The old index claimed thread ids were globally unique. They are unique per group,
-- and enforcing more than that wedged a new user out of support whenever a fresh
-- group happened to reuse a thread id an old group had already handed out.
DROP INDEX IF EXISTS idx_tg_support_topic;
CREATE UNIQUE INDEX idx_tg_support_topic ON tg_support_topics(group_id, topic_id);
