package org

import (
	"context"
	"net/http"
)

// contentBytes is what aid has stored on the board and in the research
// space: live item titles, bodies and notes, comments, project titles and
// descriptions, and notes, in bytes.
func (s *Service) contentBytes(ctx context.Context, aid string) (int64, error) {
	var n int64
	err := s.db.QueryRowContext(ctx, `SELECT
		(SELECT COALESCE(SUM(length(CAST(title AS BLOB)) + length(CAST(body AS BLOB)) + length(CAST(note AS BLOB))), 0)
			FROM board_items WHERE author_aid = ? AND status <> 'deleted') +
		(SELECT COALESCE(SUM(length(CAST(text AS BLOB))), 0) FROM board_comments WHERE author_aid = ?) +
		(SELECT COALESCE(SUM(length(CAST(title AS BLOB)) + length(CAST(description AS BLOB))), 0) FROM projects WHERE owner_aid = ?) +
		(SELECT COALESCE(SUM(length(CAST(title AS BLOB)) + length(CAST(text AS BLOB)) + length(CAST(url AS BLOB))), 0)
			FROM notes WHERE author_aid = ?)`, aid, aid, aid, aid).Scan(&n)
	if err != nil {
		return 0, s.internal("quota", err)
	}
	return n, nil
}

// checkQuota refuses a write of add bytes that would take aid over the
// per-member content quota.
func (s *Service) checkQuota(ctx context.Context, aid string, add int) error {
	used, err := s.contentBytes(ctx, aid)
	if err != nil {
		return err
	}
	if used+int64(add) > s.cfg.MemberContentBytes {
		return newErr(http.StatusConflict, CodeQuotaExceeded,
			"The member's stored content would exceed %d bytes. Delete old notes or board items first.", s.cfg.MemberContentBytes)
	}
	return nil
}
