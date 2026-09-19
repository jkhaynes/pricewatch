CREATE TABLE IF NOT EXISTS runs (
  id          INTEGER PRIMARY KEY,
  started_at  TIMESTAMP NOT NULL,
  finished_at TIMESTAMP,
  ok_count    INTEGER NOT NULL DEFAULT 0,
  error_count INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS observations (
  id           INTEGER PRIMARY KEY,
  run_id       INTEGER NOT NULL REFERENCES runs(id),
  card_id      TEXT NOT NULL,          -- collection key
  source       TEXT NOT NULL,
  market_price REAL,
  low_price    REAL,
  high_price   REAL,
  observed_at  TIMESTAMP NOT NULL,
  UNIQUE (run_id, card_id)             -- FR-6: one observation per card per run
);

CREATE INDEX IF NOT EXISTS idx_observations_card_id ON observations(card_id, id DESC);

CREATE TABLE IF NOT EXISTS collection (
  id             INTEGER PRIMARY KEY,
  collection_key TEXT NOT NULL,
  tcg_region     TEXT NOT NULL,
  card_name      TEXT NOT NULL,
  card_number    TEXT NOT NULL,
  sort_number    INTEGER,
  expansion      TEXT NOT NULL,
  rarity         TEXT,
  variant        TEXT NOT NULL,
  language       TEXT,
  condition      TEXT,
  quantity       INTEGER NOT NULL DEFAULT 1,
  tcgc_price     REAL,
  note           TEXT
);

CREATE INDEX IF NOT EXISTS idx_collection_key ON collection(collection_key);

CREATE TABLE IF NOT EXISTS card_map (
  collection_key TEXT NOT NULL,
  source         TEXT NOT NULL,
  source_card_id TEXT,              -- shared by every variant of one source card
  variant        TEXT,
  status         TEXT NOT NULL CHECK (status IN ('resolved','ambiguous','unmatched')),
  reason         TEXT,
  resolved_at    TIMESTAMP,
  PRIMARY KEY (collection_key, source) -- each provider keeps its own mappings
);

-- Stalest fetches every row of the picked source cards.
CREATE INDEX IF NOT EXISTS idx_card_map_source_card ON card_map(source, source_card_id);

CREATE TABLE IF NOT EXISTS quota (
  source TEXT NOT NULL,
  day    TEXT NOT NULL,               -- UTC date, "2026-09-18"
  used   INTEGER NOT NULL,
  PRIMARY KEY (source, day)
);

-- Card art for the status page (DD-14), one per source card.
CREATE TABLE IF NOT EXISTS card_art (
  source         TEXT NOT NULL,
  source_card_id TEXT NOT NULL,
  image_url      TEXT NOT NULL,
  PRIMARY KEY (source, source_card_id)
);
