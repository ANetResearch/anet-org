package org

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"time"
)

// Report limits.
const (
	maxReportReason   = 1000
	maxReportEvidence = 4400
	// maxOpenReports bounds the open reports one member has filed.
	maxOpenReports = 20
	// ReportRetention is how long a closed report, with the text it copied,
	// is kept.
	ReportRetention = 90 * 24 * time.Hour
)

// Report subjects.
const (
	subjectMember    = "member"
	subjectMessage   = "message"
	subjectBoardItem = "board_item"
	subjectComment   = "comment"
)

type reportArgs struct {
	Handle    string `json:"handle"`
	MessageID int64  `json:"message_id"`
	BoardItem int64  `json:"board_item"`
	CommentID int64  `json:"comment_id"`
	Reason    string `json:"reason"`
}

// orgReport files a report about a member, a message in the caller's
// mailbox, a board item or a comment, for the administrators. The text the
// report is about is copied into it, so that it stays available to them
// after the message is acknowledged or the item deleted.
func (s *Service) orgReport(ctx context.Context, c *call) (any, error) {
	var a reportArgs
	if err := decode(c.raw, &a); err != nil {
		return nil, err
	}
	reason, err := text("reason", a.Reason, maxReportReason, true)
	if err != nil {
		return nil, err
	}
	n := 0
	for _, set := range []bool{strings.TrimSpace(a.Handle) != "", a.MessageID != 0, a.BoardItem != 0, a.CommentID != 0} {
		if set {
			n++
		}
	}
	if n != 1 {
		return nil, newErr(http.StatusBadRequest, CodeBadArgs,
			`Name exactly one of "handle", "message_id", "board_item" or "comment_id".`)
	}
	var subject, evidence, target string
	var subjectID int64
	switch {
	case a.Handle != "":
		if len(a.Handle) > 64 {
			return nil, errTooLong("handle", 64)
		}
		m, err := s.memberByHandle(ctx, normalizeHandle(a.Handle))
		if err != nil {
			return nil, err
		}
		if m == nil {
			return nil, newErr(http.StatusNotFound, CodeMemberUnknown, "No member has the handle %q.", normalizeHandle(a.Handle))
		}
		subject, target = subjectMember, m.AID
	case a.MessageID != 0:
		var from string
		err := s.db.QueryRowContext(ctx, `SELECT from_aid, text FROM messages WHERE id = ? AND to_aid = ?`,
			a.MessageID, c.member.AID).Scan(&from, &evidence)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, errInvalid("message_id", "no message with this id is in the caller's mailbox.")
		}
		if err != nil {
			return nil, s.internal("report", err)
		}
		if strings.HasPrefix(from, boardSender) {
			return nil, errInvalid("message_id", "board notifications are not written by members; report the board item or comment.")
		}
		subject, subjectID, target = subjectMessage, a.MessageID, from
	case a.BoardItem != 0:
		var title, body, status string
		err := s.db.QueryRowContext(ctx, `SELECT author_aid, title, body, status FROM board_items WHERE id = ?`,
			a.BoardItem).Scan(&target, &title, &body, &status)
		if errors.Is(err, sql.ErrNoRows) || status == statusDeleted {
			return nil, newErr(http.StatusNotFound, CodeItemUnknown, "No board item has the id %d.", a.BoardItem)
		}
		if err != nil {
			return nil, s.internal("report", err)
		}
		evidence = title
		if body != "" {
			evidence += "\n\n" + body
		}
		subject, subjectID = subjectBoardItem, a.BoardItem
	default:
		err := s.db.QueryRowContext(ctx, `SELECT bc.author_aid, bc.text FROM board_comments bc
			JOIN board_items i ON i.id = bc.item_id WHERE bc.id = ? AND i.status <> ?`,
			a.CommentID, statusDeleted).Scan(&target, &evidence)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, errInvalid("comment_id", "no comment has this id.")
		}
		if err != nil {
			return nil, s.internal("report", err)
		}
		subject, subjectID = subjectComment, a.CommentID
	}
	if target == c.member.AID {
		return nil, errInvalid("report", "a member cannot report itself.")
	}
	if len(evidence) > maxReportEvidence {
		evidence = truncateUTF8(evidence, maxReportEvidence)
	}
	targetHandle := ""
	if m, err := s.memberByAID(ctx, target); err != nil {
		return nil, err
	} else if m != nil {
		targetHandle = m.Handle
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, s.internal("report", err)
	}
	defer tx.Rollback()
	var existing int64
	err = tx.QueryRowContext(ctx, `SELECT id FROM reports WHERE reporter_aid = ? AND subject = ? AND subject_id = ?
		AND target_aid = ? AND closed_at IS NULL`, c.member.AID, subject, subjectID, target).Scan(&existing)
	if err == nil {
		return map[string]any{"reported": true, "report_id": existing, "duplicate": true,
			"note": "This report is already open; the administrators will look at it."}, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, s.internal("report", err)
	}
	var open int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM reports WHERE reporter_aid = ? AND closed_at IS NULL`,
		c.member.AID).Scan(&open); err != nil {
		return nil, s.internal("report", err)
	}
	if open >= maxOpenReports {
		return nil, newErr(http.StatusConflict, CodeLimitReached,
			"The caller has %d open reports. Wait until the administrators have closed some of them.", open)
	}
	res, err := tx.ExecContext(ctx, `INSERT INTO reports(reporter_aid, reporter_handle, target_aid, target_handle, subject,
		subject_id, evidence, reason, created_at) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		c.member.AID, c.member.Handle, target, targetHandle, subject, subjectID, evidence, reason, s.now().Unix())
	if err != nil {
		return nil, s.internal("report", err)
	}
	id, _ := res.LastInsertId()
	if err := tx.Commit(); err != nil {
		return nil, s.internal("report", err)
	}
	s.log.Info("report filed", "id", id, "reporter", c.member.AID, "target", target, "subject", subject)
	return map[string]any{"reported": true, "report_id": id,
		"note": "The administrators of " + s.cfg.Org.Name + " will look at the report. The member reported is not told who reported it."}, nil
}

// truncateUTF8 cuts s to at most max bytes on a character boundary.
func truncateUTF8(s string, max int) string {
	if len(s) <= max {
		return s
	}
	cut := max
	for cut > 0 && (s[cut]&0xC0) == 0x80 {
		cut--
	}
	return s[:cut]
}

// ReportView is a report as the administrators read it. Reason and evidence
// were written by members.
type ReportView struct {
	ID                int64  `json:"id"`
	Subject           string `json:"subject"`
	SubjectID         int64  `json:"subject_id,omitempty"`
	Reporter          string `json:"reporter"`
	Target            string `json:"target"`
	TargetAID         string `json:"target_aid"`
	TargetIsMember    bool   `json:"target_is_member"`
	TargetOpenReports int    `json:"target_open_reports"`
	CreatedAt         string `json:"created_at"`
	ClosedAt          string `json:"closed_at,omitempty"`
	ClosedBy          string `json:"closed_by,omitempty"`
	CloseNote         string `json:"close_note,omitempty"`
	UntrustedReason   string `json:"untrusted_reason"`
	UntrustedEvidence string `json:"untrusted_evidence,omitempty"`
}

type reportListArgs struct {
	Status string `json:"status"`
	Limit  int    `json:"limit"`
	Before int64  `json:"before"`
}

// pruneReports deletes the reports closed more than ReportRetention ago.
func (s *Service) pruneReports(ctx context.Context) error {
	cut := s.now().Add(-ReportRetention).Unix()
	if _, err := s.db.ExecContext(ctx, `DELETE FROM reports WHERE closed_at IS NOT NULL AND closed_at < ?`, cut); err != nil {
		return s.internal("report.prune", err)
	}
	return nil
}

func (s *Service) adminReportList(ctx context.Context, c *call) (any, error) {
	var a reportListArgs
	if err := decode(c.raw, &a); err != nil {
		return nil, err
	}
	if err := s.pruneReports(ctx); err != nil {
		return nil, err
	}
	cond := "r.closed_at IS NULL"
	switch a.Status {
	case "", "open":
	case "closed":
		cond = "r.closed_at IS NOT NULL"
	case "all":
		cond = "1 = 1"
	default:
		return nil, errInvalid("status", "use open, closed or all.")
	}
	if a.Before < 0 {
		return nil, errInvalid("before", "use a report id.")
	}
	n := limit(a.Limit, 50, 200)
	before := a.Before
	if before == 0 {
		before = 1 << 62
	}
	rows, err := s.db.QueryContext(ctx, `SELECT r.id, r.subject, r.subject_id, COALESCE(rm.handle, r.reporter_handle),
		COALESCE(tm.handle, r.target_handle), r.target_aid, tm.aid IS NOT NULL,
		(SELECT COUNT(*) FROM reports o WHERE o.target_aid = r.target_aid AND o.closed_at IS NULL),
		r.created_at, COALESCE(r.closed_at, 0), r.closed_by, r.close_note, r.reason, r.evidence
		FROM reports r LEFT JOIN members rm ON rm.aid = r.reporter_aid LEFT JOIN members tm ON tm.aid = r.target_aid
		WHERE `+cond+` AND r.id < ? ORDER BY r.id DESC LIMIT ?`, before, n+1)
	if err != nil {
		return nil, s.internal("report.list", err)
	}
	defer rows.Close()
	out := []ReportView{}
	more := false
	for rows.Next() {
		var v ReportView
		var created, closed int64
		if err := rows.Scan(&v.ID, &v.Subject, &v.SubjectID, &v.Reporter, &v.Target, &v.TargetAID, &v.TargetIsMember,
			&v.TargetOpenReports, &created, &closed, &v.ClosedBy, &v.CloseNote, &v.UntrustedReason, &v.UntrustedEvidence); err != nil {
			return nil, s.internal("report.list", err)
		}
		if len(out) == n {
			more = true
			break
		}
		if v.Reporter == "" {
			v.Reporter = formerMember
		}
		if v.Target == "" {
			v.Target = formerMember
		}
		v.CreatedAt, v.ClosedAt = ts(created), ts(closed)
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		return nil, s.internal("report.list", err)
	}
	return map[string]any{"notice": "Fields named untrusted_* were written by members. Remove a member with admin.member.remove " +
		"(ban: true keeps the identity out), then close the report with admin.report.close.", "reports": out, "more": more}, nil
}

type reportCloseArgs struct {
	ID   int64  `json:"id"`
	Note string `json:"note"`
}

func (s *Service) adminReportClose(ctx context.Context, c *call) (any, error) {
	var a reportCloseArgs
	if err := decode(c.raw, &a); err != nil {
		return nil, err
	}
	if a.ID <= 0 {
		return nil, errMissing("id")
	}
	note, err := text("note", a.Note, 500, false)
	if err != nil {
		return nil, err
	}
	res, err := s.db.ExecContext(ctx, `UPDATE reports SET closed_at = ?, closed_by = ?, close_note = ? WHERE id = ? AND closed_at IS NULL`,
		s.now().Unix(), adminName(c), note, a.ID)
	if err != nil {
		return nil, s.internal("report.close", err)
	}
	if k, _ := res.RowsAffected(); k == 0 {
		var exists int
		if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM reports WHERE id = ?`, a.ID).Scan(&exists); err != nil {
			return nil, s.internal("report.close", err)
		}
		if exists == 0 {
			return nil, newErr(http.StatusNotFound, CodeReportUnknown, "No report has the id %d.", a.ID)
		}
		return map[string]any{"closed": true, "id": a.ID, "already_closed": true}, nil
	}
	return map[string]any{"closed": true, "id": a.ID}, nil
}
