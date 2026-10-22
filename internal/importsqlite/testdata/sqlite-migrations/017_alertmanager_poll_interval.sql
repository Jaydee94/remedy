-- poll_interval used to pace both GitHub and Alertmanager; Alertmanager now has the setting alertmanager_poll_interval. A
-- maintainer who changed poll_interval keeps the same pace for Alertmanager until he changes the new setting, so the value is
-- copied once. Nothing is added when poll_interval was never set (the default of both is one minute), and a row that already
-- exists is not overwritten.
INSERT INTO settings (key, value, updated_at)
SELECT 'alertmanager_poll_interval', value, updated_at FROM settings
WHERE key = 'poll_interval'
  AND NOT EXISTS (SELECT 1 FROM settings WHERE key = 'alertmanager_poll_interval');
