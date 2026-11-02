-- The Idempotency-Key is global (fleet REST guideline 2.3): a stored row is
-- looked up by key alone and compared by request fingerprint (method + path +
-- body), so the same key on another route is key reuse (409), not a new key.
-- Rows older than the key lifetime are dropped and, where a key was used on
-- several routes, only the newest row survives, so the new primary key holds.
DELETE FROM idempotency_keys a
 USING idempotency_keys b
 WHERE a.key = b.key
   AND (a.created_at, a.route) < (b.created_at, b.route);

ALTER TABLE idempotency_keys DROP CONSTRAINT idempotency_keys_pkey;
ALTER TABLE idempotency_keys ADD PRIMARY KEY (key);
