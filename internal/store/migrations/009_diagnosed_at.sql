-- When the stored diagnosis was written. A new diagnosis moves it; a diagnosis that was started and failed does not (it moves last_diagnosis_at only).
ALTER TABLE incidents ADD COLUMN diagnosed_at TEXT;
-- The best value the old data has: the start of the last diagnosis, which is what Today's digest used. For an incident whose last
-- diagnosis failed this is the start of that failed attempt: it is counted the old way for at most 24 hours, and right from then on.
UPDATE incidents SET diagnosed_at = last_diagnosis_at WHERE diagnosis IS NOT NULL;
