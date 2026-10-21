package org

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

// Message limits.
const (
	maxMessageText = 4000
	maxAckIDs      = 500
)

// Groups msg.send understands, besides handles.
const (
	groupStaff = "staff" // every staff member; any member may write to it
	groupAll   = "all"   // every member; staff and administrators only
)

type sendArgs struct {
	To   string `json:"to"`
	Text string `json:"text"`
}

func (s *Service) msgSend(ctx context.Context, c *call) (any, error) {
	var a sendArgs
	if err := decode(c.raw, &a); err != nil {
		return nil, err
	}
	return s.send(ctx, c.member, a)
}

// send stores a message in each recipient's mailbox.
func (s *Service) send(ctx context.Context, from *Member, a sendArgs) (map[string]any, error) {
	to := normalizeHandle(a.To)
	if to == "" {
		return nil, errMissing("to")
	}
	if len(a.To) > 64 {
		return nil, errTooLong("to", 64)
	}
	body, err := text("text", a.Text, maxMessageText, true)
	if err != nil {
		return nil, err
	}
	var recipients []*Member
	group := ""
	switch to {
	case groupStaff, groupAll:
		group = to
		if to == groupAll && !s.isStaff(from) {
			return nil, errNotPermitted("staff and administrators")
		}
		rows, err := s.db.QueryContext(ctx, `SELECT aid, handle, kind FROM members ORDER BY handle`)
		if err != nil {
			return nil, s.internal("send", err)
		}
		for rows.Next() {
			m := &Member{}
			if err := rows.Scan(&m.AID, &m.Handle, &m.Kind); err != nil {
				rows.Close()
				return nil, s.internal("send", err)
			}
			if m.AID == from.AID {
				continue
			}
			if to == groupAll || s.isStaff(m) {
				recipients = append(recipients, m)
			}
		}
		rows.Close()
		if len(recipients) == 0 {
			return nil, newErr(http.StatusNotFound, CodeRecipientUnknown, "The group %q has no other members.", to)
		}
	default:
		m, err := s.memberByHandle(ctx, to)
		if err != nil {
			return nil, err
		}
		if m == nil {
			return nil, newErr(http.StatusNotFound, CodeRecipientUnknown,
				"No member has the handle %q. Use org.members to find handles.", to)
		}
		recipients = []*Member{m}
	}

	now := s.now().Unix()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, s.internal("send", err)
	}
	defer tx.Rollback()
	ids := make([]int64, 0, len(recipients))
	delivered := 0
	var refusal *Error
	for _, r := range recipients {
		// Nothing already in a mailbox is ever evicted: a full mailbox, or
		// too many unread messages from this sender, refuses the new one.
		if e, err := s.roomFor(ctx, tx, from.AID, r); err != nil {
			return nil, err
		} else if e != nil {
			refusal = e
			continue
		}
		res, err := tx.ExecContext(ctx, `INSERT INTO messages(to_aid, from_aid, from_handle, via_group, text, sent_at)
			VALUES(?, ?, ?, ?, ?, ?)`, r.AID, from.AID, from.Handle, group, body, now)
		if err != nil {
			return nil, s.internal("send", err)
		}
		id, _ := res.LastInsertId()
		ids = append(ids, id)
		delivered++
	}
	if delivered == 0 {
		return nil, refusal
	}
	if err := tx.Commit(); err != nil {
		return nil, s.internal("send", err)
	}
	out := map[string]any{"sent": true, "recipients": delivered}
	if group != "" {
		out["to"] = "group:" + group
		out["skipped_full_mailboxes"] = len(recipients) - delivered
	} else {
		out["to"] = recipients[0].Handle
		out["message_id"] = ids[0]
	}
	return out, nil
}

// roomFor returns a refusal when r's mailbox cannot take another message
// from the sender: the mailbox holds MailboxMax unread messages, or the
// sender already has MailboxPerSender unread messages in it.
func (s *Service) roomFor(ctx context.Context, tx *sql.Tx, sender string, r *Member) (*Error, error) {
	var total, mine int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(from_aid = ?), 0) FROM messages WHERE to_aid = ?`,
		sender, r.AID).Scan(&total, &mine); err != nil {
		return nil, s.internal("send", err)
	}
	if total >= s.cfg.MailboxMax {
		return newErr(http.StatusConflict, CodeMailboxFull,
			"The mailbox of %s is full. The message was not stored; nothing was removed from the mailbox.", r.Handle), nil
	}
	if mine >= s.cfg.MailboxPerSender {
		return newErr(http.StatusConflict, CodeTooManyUnread,
			"%s has %d unread messages from the caller. Wait until they are read.", r.Handle, mine), nil
	}
	return nil, nil
}

type inboxArgs struct {
	Since json.RawMessage `json:"since"`
	Limit int             `json:"limit"`
}

// InboxMessage is one message as its recipient reads it. The text is in
// untrusted_text: it was written by another member.
type InboxMessage struct {
	ID            int64  `json:"id"`
	From          string `json:"from"`
	FromKind      string `json:"from_kind"`
	ViaGroup      string `json:"via_group,omitempty"`
	SentAt        string `json:"sent_at"`
	UntrustedText string `json:"untrusted_text"`
}

func (s *Service) msgInbox(ctx context.Context, c *call) (any, error) {
	var a inboxArgs
	if err := decode(c.raw, &a); err != nil {
		return nil, err
	}
	return s.inbox(ctx, c.member, a)
}

// inbox lists the member's mailbox, oldest first. since is a message id
// (only later ids) or an RFC 3339 time (only messages sent at or after it).
func (s *Service) inbox(ctx context.Context, m *Member, a inboxArgs) (map[string]any, error) {
	var sinceID, sinceTime int64
	if raw := strings.TrimSpace(string(a.Since)); raw != "" && raw != "null" {
		var id int64
		var t string
		switch {
		case json.Unmarshal(a.Since, &id) == nil:
			if id < 0 {
				return nil, errInvalid("since", "use a message id or an RFC 3339 time.")
			}
			sinceID = id
		case json.Unmarshal(a.Since, &t) == nil:
			pt, err := time.Parse(time.RFC3339, t)
			if err != nil {
				return nil, errInvalid("since", "use a message id or an RFC 3339 time.")
			}
			sinceTime = pt.Unix()
		default:
			return nil, errInvalid("since", "use a message id or an RFC 3339 time.")
		}
	}
	n := limit(a.Limit, 50, 200)
	rows, err := s.db.QueryContext(ctx, `SELECT msg.id, COALESCE(fm.handle, msg.from_handle), COALESCE(fm.kind, ''),
		fm.aid, msg.via_group, msg.sent_at, msg.text
		FROM messages msg LEFT JOIN members fm ON fm.aid = msg.from_aid
		WHERE msg.to_aid = ? AND msg.id > ? AND msg.sent_at >= ? ORDER BY msg.id ASC LIMIT ?`,
		m.AID, sinceID, sinceTime, n+1)
	if err != nil {
		return nil, s.internal("inbox", err)
	}
	defer rows.Close()
	out := []InboxMessage{}
	more := false
	for rows.Next() {
		var im InboxMessage
		var kind string
		var fromAID sql.NullString
		var sent int64
		if err := rows.Scan(&im.ID, &im.From, &kind, &fromAID, &im.ViaGroup, &sent, &im.UntrustedText); err != nil {
			return nil, s.internal("inbox", err)
		}
		if len(out) == n {
			more = true
			break
		}
		if fromAID.Valid {
			im.FromKind = s.kindOf(&Member{AID: fromAID.String, Kind: kind})
		} else {
			im.FromKind = "former_member"
		}
		im.SentAt = ts(sent)
		out = append(out, im)
	}
	if err := rows.Err(); err != nil {
		return nil, s.internal("inbox", err)
	}
	// The pool holds one connection: the rows must be closed before the
	// next statement.
	rows.Close()
	var total int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM messages WHERE to_aid = ?`, m.AID).Scan(&total); err != nil {
		return nil, s.internal("inbox", err)
	}
	res := map[string]any{
		"notice":   untrustedNotice + " Acknowledge read messages with msg.ack to remove them from the mailbox.",
		"messages": out,
		"more":     more,
		"mailbox":  map[string]any{"messages": total, "limit": s.cfg.MailboxMax},
	}
	return res, nil
}

type ackArgs struct {
	IDs  []int64 `json:"ids"`
	UpTo int64   `json:"up_to"`
}

func (s *Service) msgAck(ctx context.Context, c *call) (any, error) {
	var a ackArgs
	if err := decode(c.raw, &a); err != nil {
		return nil, err
	}
	if len(a.IDs) == 0 && a.UpTo <= 0 {
		return nil, newErr(http.StatusBadRequest, CodeMissingField, `Give "ids" (a list of message ids) or "up_to" (a message id).`)
	}
	if len(a.IDs) > maxAckIDs {
		return nil, errInvalid("ids", "at most 500 ids per call.")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, s.internal("ack", err)
	}
	defer tx.Rollback()
	var n int64
	for _, id := range a.IDs {
		res, err := tx.ExecContext(ctx, `DELETE FROM messages WHERE id = ? AND to_aid = ?`, id, c.member.AID)
		if err != nil {
			return nil, s.internal("ack", err)
		}
		k, _ := res.RowsAffected()
		n += k
	}
	if a.UpTo > 0 {
		res, err := tx.ExecContext(ctx, `DELETE FROM messages WHERE id <= ? AND to_aid = ?`, a.UpTo, c.member.AID)
		if err != nil {
			return nil, s.internal("ack", err)
		}
		k, _ := res.RowsAffected()
		n += k
	}
	if err := tx.Commit(); err != nil {
		return nil, s.internal("ack", err)
	}
	return map[string]any{"acknowledged": n}, nil
}
