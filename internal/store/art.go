package store

import (
	"context"
	"fmt"
)

// PutArt records a source card's art URL, replacing any earlier one.
func (s *SQLite) PutArt(ctx context.Context, source, sourceCardID, url string) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO card_art (source, source_card_id, image_url) VALUES (?,?,?)
		ON CONFLICT(source, source_card_id) DO UPDATE SET image_url = excluded.image_url`,
		source, sourceCardID, url)
	if err != nil {
		return fmt.Errorf("put art %s/%s: %w", source, sourceCardID, err)
	}
	return nil
}
