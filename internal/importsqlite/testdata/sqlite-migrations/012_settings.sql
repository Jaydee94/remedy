-- Settings the maintainer changes in the Setup page. The registry in the code knows the keys; a row whose key or value the
-- registry no longer accepts is ignored at start-up. value is JSON: a string for a duration, a number, an array of strings.
CREATE TABLE settings (
  key        TEXT PRIMARY KEY,
  value      TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
