package org

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"unicode/utf8"
)

// Board limits.
const (
	maxItemTitle     = 200
	maxItemBody      = 4000
	maxItemNote      = 1000
	maxCommentText   = 2000
	maxTags          = 8
	maxCommentsPerIt = 500
)

// Item states.
const (
	statusOpen      = "open"
	statusClaimed   = "claimed"
	statusDone      = "done"
	statusWithdrawn = "withdrawn"
	statusDeleted   = "deleted"
)

// boardQueryWindow bounds the items a text query scans: the newest ones.
const boardQueryWindow = 2000

var tagRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]{0,31}$`)

// ItemView is a board item as members see it. Text written by members is
// in the untrusted_* fields.
type ItemView struct {
	ID             int64         `json:"id"`
	Status         string        `json:"status"`
	Author         string        `json:"author"`
	Claimer        string        `json:"claimer,omitempty"`
	Tags           []string      `json:"tags"`
	CreatedAt      string        `json:"created_at"`
	UpdatedAt      string        `json:"updated_at"`
	UntrustedTitle string        `json:"untrusted_title"`
	UntrustedBody  string        `json:"untrusted_body,omitempty"`
	UntrustedNote  string        `json:"untrusted_note,omitempty"`
	CommentCount   int           `json:"comment_count"`
	Comments       []CommentView `json:"comments,omitempty"`
	// Via, ClaimVia and CloseVia mark the post, the claim and the closing
	// (done or withdrawn, with its note) made by the organisation's agent.
	Via        string `json:"via,omitempty"`
	ClaimVia   string `json:"claim_via,omitempty"`
	CloseVia   string `json:"close_via,omitempty"`
	authorAID  string
	claimerAID string
}

// CommentView is one comment.
type CommentView struct {
	ID            int64  `json:"id"`
	Author        string `json:"author"`
	Via           string `json:"via,omitempty"`
	CreatedAt     string `json:"created_at"`
	UntrustedText string `json:"untrusted_text"`
}

const formerMember = "(former member)"

const itemSelect = `SELECT i.id, i.status, COALESCE(a.handle, ''), i.author_aid, COALESCE(cl.handle, ''),
	COALESCE(i.claimer_aid, ''), i.tags, i.created_at, i.updated_at, i.title, i.body, i.note,
	(SELECT COUNT(*) FROM board_comments bc WHERE bc.item_id = i.id), i.via, i.claim_via, i.close_via
	FROM board_items i
	LEFT JOIN members a ON a.aid = i.author_aid
	LEFT JOIN members cl ON cl.aid = i.claimer_aid`

func scanItem(sc interface{ Scan(...any) error }) (*ItemView, error) {
	var v ItemView
	var tags string
	var created, updated int64
	var via, claimVia, closeVia string
	if err := sc.Scan(&v.ID, &v.Status, &v.Author, &v.authorAID, &v.Claimer, &v.claimerAID, &tags, &created, &updated,
		&v.UntrustedTitle, &v.UntrustedBody, &v.UntrustedNote, &v.CommentCount, &via, &claimVia, &closeVia); err != nil {
		return nil, err
	}
	v.Via = shownVia(via)
	if v.claimerAID != "" {
		v.ClaimVia = shownVia(claimVia)
	}
	if v.Status == statusDone || v.Status == statusWithdrawn {
		v.CloseVia = shownVia(closeVia)
	}
	if v.Author == "" {
		v.Author = formerMember
	}
	if v.Claimer == "" && v.claimerAID != "" {
		v.Claimer = formerMember
	}
	v.Tags = []string{}
	if tags != "" {
		v.Tags = strings.Split(tags, ",")
	}
	v.CreatedAt, v.UpdatedAt = ts(created), ts(updated)
	return &v, nil
}

func (s *Service) item(ctx context.Context, id int64) (*ItemView, error) {
	v, err := scanItem(s.db.QueryRowContext(ctx, itemSelect+` WHERE i.id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, newErr(http.StatusNotFound, CodeItemUnknown, "No board item has the id %d.", id)
	}
	if err != nil {
		return nil, s.internal("board", err)
	}
	if v.Status == statusDeleted {
		return nil, newErr(http.StatusNotFound, CodeItemUnknown, "No board item has the id %d.", id)
	}
	return v, nil
}

func checkTags(in []string) ([]string, error) {
	if len(in) > maxTags {
		return nil, errInvalid("tags", "at most 8 tags.")
	}
	seen := map[string]bool{}
	out := []string{}
	for _, t := range in {
		t = strings.ToLower(strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(t), "#")))
		if !tagRe.MatchString(t) {
			return nil, errInvalid("tags", "a tag has 1 to 32 characters: lower-case letters, digits, '_', '.' and '-'.")
		}
		if !seen[t] {
			seen[t] = true
			out = append(out, t)
		}
	}
	return out, nil
}

// --- board.post ---

type postArgs struct {
	Title string   `json:"title"`
	Body  string   `json:"body"`
	Tags  []string `json:"tags"`
}

func (s *Service) boardPost(ctx context.Context, c *call) (any, error) {
	var a postArgs
	if err := decode(c.raw, &a); err != nil {
		return nil, err
	}
	return s.post(ctx, c.member, a, c.via())
}

func (s *Service) post(ctx context.Context, m *Member, a postArgs, via string) (map[string]any, error) {
	title, err := text("title", a.Title, maxItemTitle, true)
	if err != nil {
		return nil, err
	}
	body, err := text("body", a.Body, maxItemBody, false)
	if err != nil {
		return nil, err
	}
	tags, err := checkTags(a.Tags)
	if err != nil {
		return nil, err
	}
	if err := s.checkQuota(ctx, m.AID, len(title)+len(body)); err != nil {
		return nil, err
	}
	now := s.now().Unix()
	res, err := s.db.ExecContext(ctx, `INSERT INTO board_items(author_aid, title, body, tags, status, created_at, updated_at, via)
		VALUES(?, ?, ?, ?, 'open', ?, ?, ?)`, m.AID, title, body, strings.Join(tags, ","), now, now, via)
	if err != nil {
		return nil, s.internal("board.post", err)
	}
	id, _ := res.LastInsertId()
	v, err := s.item(ctx, id)
	if err != nil {
		return nil, err
	}
	return map[string]any{"item": v}, nil
}

// --- board.list ---

type boardListArgs struct {
	ID     int64  `json:"id"`
	Status string `json:"status"`
	Tag    string `json:"tag"`
	Mine   bool   `json:"mine"`
	Query  string `json:"query"`
	Limit  int    `json:"limit"`
	Before int64  `json:"before"`
}

func (s *Service) boardList(ctx context.Context, c *call) (any, error) {
	var a boardListArgs
	if err := decode(c.raw, &a); err != nil {
		return nil, err
	}
	return s.listBoard(ctx, c.member, a)
}

func (s *Service) listBoard(ctx context.Context, m *Member, a boardListArgs) (map[string]any, error) {
	if a.ID > 0 {
		v, err := s.item(ctx, a.ID)
		if err != nil {
			return nil, err
		}
		if v.Status == statusWithdrawn && v.authorAID != m.AID && !s.isStaff(m) {
			return nil, newErr(http.StatusNotFound, CodeItemUnknown, "No board item has the id %d.", a.ID)
		}
		rows, err := s.db.QueryContext(ctx, `SELECT bc.id, COALESCE(mm.handle, ''), bc.created_at, bc.text, bc.via
			FROM board_comments bc LEFT JOIN members mm ON mm.aid = bc.author_aid
			WHERE bc.item_id = ? ORDER BY bc.id ASC LIMIT ?`, a.ID, maxCommentsPerIt)
		if err != nil {
			return nil, s.internal("board.list", err)
		}
		defer rows.Close()
		v.Comments = []CommentView{}
		for rows.Next() {
			var cv CommentView
			var at int64
			var via string
			if err := rows.Scan(&cv.ID, &cv.Author, &at, &cv.UntrustedText, &via); err != nil {
				return nil, s.internal("board.list", err)
			}
			cv.Via = shownVia(via)
			if cv.Author == "" {
				cv.Author = formerMember
			}
			cv.CreatedAt = ts(at)
			v.Comments = append(v.Comments, cv)
		}
		return map[string]any{"notice": untrustedNotice, "items": []*ItemView{v}, "more": false}, rows.Err()
	}
	where := []string{}
	args := []any{}
	switch a.Status {
	case "", "active":
		where = append(where, "i.status IN ('open', 'claimed')")
	case statusOpen, statusClaimed, statusDone:
		where = append(where, "i.status = ?")
		args = append(args, a.Status)
	case statusWithdrawn:
		if !s.isStaff(m) {
			where = append(where, "i.status = 'withdrawn' AND i.author_aid = ?")
			args = append(args, m.AID)
		} else {
			where = append(where, "i.status = 'withdrawn'")
		}
	case "all":
		where = append(where, "i.status IN ('open', 'claimed', 'done')")
	default:
		return nil, errInvalid("status", "use active, open, claimed, done, withdrawn or all.")
	}
	if a.Tag != "" {
		tags, err := checkTags([]string{a.Tag})
		if err != nil {
			return nil, err
		}
		where = append(where, "(',' || i.tags || ',') LIKE ? ESCAPE '\\'")
		args = append(args, likeEscape(","+tags[0]+","))
	}
	if a.Mine {
		where = append(where, "(i.author_aid = ? OR i.claimer_aid = ?)")
		args = append(args, m.AID, m.AID)
	}
	if a.Query != "" {
		q, err := text("query", a.Query, 100, false)
		if err != nil {
			return nil, err
		}
		// Only the newest items are searched, so a query costs a bounded scan.
		where = append(where, "i.id >= (SELECT COALESCE(MIN(id), 0) FROM (SELECT id FROM board_items ORDER BY id DESC LIMIT ?))",
			"(i.title LIKE ? ESCAPE '\\' OR i.body LIKE ? ESCAPE '\\')")
		args = append(args, boardQueryWindow, likeEscape(q), likeEscape(q))
	}
	if a.Before > 0 {
		where = append(where, "i.id < ?")
		args = append(args, a.Before)
	}
	n := limit(a.Limit, 30, 100)
	args = append(args, n+1)
	rows, err := s.db.QueryContext(ctx, itemSelect+" WHERE "+strings.Join(where, " AND ")+" ORDER BY i.id DESC LIMIT ?", args...)
	if err != nil {
		return nil, s.internal("board.list", err)
	}
	defer rows.Close()
	out := []*ItemView{}
	more := false
	for rows.Next() {
		v, err := scanItem(rows)
		if err != nil {
			return nil, s.internal("board.list", err)
		}
		if len(out) == n {
			more = true
			break
		}
		out = append(out, v)
	}
	return map[string]any{"notice": untrustedNotice, "items": out, "more": more}, rows.Err()
}

// --- board.claim ---

type claimArgs struct {
	ID      int64 `json:"id"`
	Release bool  `json:"release"`
}

func (s *Service) boardClaim(ctx context.Context, c *call) (any, error) {
	var a claimArgs
	if err := decode(c.raw, &a); err != nil {
		return nil, err
	}
	if a.ID <= 0 {
		return nil, errMissing("id")
	}
	m := c.member
	v, err := s.item(ctx, a.ID)
	if err != nil {
		return nil, err
	}
	now := s.now().Unix()
	if a.Release {
		if v.Status != statusClaimed {
			return nil, newErr(http.StatusConflict, CodeItemClosed, "The item is not claimed.")
		}
		if v.claimerAID != m.AID && !s.isStaff(m) {
			return nil, errNotPermitted("the member who claimed the item, or staff,")
		}
		if _, err := s.db.ExecContext(ctx, `UPDATE board_items SET status = 'open', claimer_aid = NULL, claim_via = '', updated_at = ?
			WHERE id = ? AND status = 'claimed'`, now, a.ID); err != nil {
			return nil, s.internal("board.claim", err)
		}
	} else {
		switch v.Status {
		case statusClaimed:
			if v.claimerAID == m.AID {
				return nil, newErr(http.StatusConflict, CodeAlreadyClaimed, "The caller has already claimed this item.")
			}
			return nil, newErr(http.StatusConflict, CodeAlreadyClaimed, "The item is already claimed by %s.", v.Claimer)
		case statusDone, statusWithdrawn:
			return nil, newErr(http.StatusConflict, CodeItemClosed, "The item is %s.", v.Status)
		}
		res, err := s.db.ExecContext(ctx, `UPDATE board_items SET status = 'claimed', claimer_aid = ?, claim_via = ?, updated_at = ?
			WHERE id = ? AND status = 'open'`, m.AID, c.via(), now, a.ID)
		if err != nil {
			return nil, s.internal("board.claim", err)
		}
		if k, _ := res.RowsAffected(); k != 1 {
			return nil, newErr(http.StatusConflict, CodeAlreadyClaimed, "The item was claimed by someone else.")
		}
	}
	prev := v
	v, err = s.item(ctx, a.ID)
	if err != nil {
		return nil, err
	}
	// The author learns of a claim and of a release by someone else; the
	// claimer of a release by staff.
	var n notice
	if a.Release {
		n = notice{text: "@" + m.Handle + " released the claim on board item " + itoa(a.ID) + ` "` + v.UntrustedTitle +
			`"; the item is open again.`}
		n.to = append(n.to, v.authorAID, prev.claimerAID)
	} else {
		n = notice{text: "@" + m.Handle + " claimed your board item " + itoa(a.ID) + ` "` + v.UntrustedTitle + `".` +
			" Agree the details by message (msg.send to @" + m.Handle + ")."}
		n.to = append(n.to, v.authorAID)
	}
	return map[string]any{"item": v, "notified": s.notify(ctx, m, a.ID, c.via(), n)}, nil
}

// --- board.done ---

type doneArgs struct {
	ID       int64  `json:"id"`
	Note     string `json:"note"`
	Withdraw bool   `json:"withdraw"`
}

func (s *Service) boardDone(ctx context.Context, c *call) (any, error) {
	var a doneArgs
	if err := decode(c.raw, &a); err != nil {
		return nil, err
	}
	if a.ID <= 0 {
		return nil, errMissing("id")
	}
	note, err := text("note", a.Note, maxItemNote, false)
	if err != nil {
		return nil, err
	}
	m := c.member
	if note != "" {
		if err := s.checkQuota(ctx, m.AID, len(note)); err != nil {
			return nil, err
		}
	}
	v, err := s.item(ctx, a.ID)
	if err != nil {
		return nil, err
	}
	if v.Status == statusDone || v.Status == statusWithdrawn {
		return nil, newErr(http.StatusConflict, CodeItemClosed, "The item is already %s.", v.Status)
	}
	target := statusDone
	if a.Withdraw {
		target = statusWithdrawn
		if v.authorAID != m.AID && !s.isStaff(m) {
			return nil, errNotPermitted("the author of the item, or staff,")
		}
	} else if v.authorAID != m.AID && v.claimerAID != m.AID && !s.isStaff(m) {
		return nil, errNotPermitted("the author or the claimer of the item, or staff,")
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE board_items SET status = ?, note = ?, close_via = ?, updated_at = ?
		WHERE id = ? AND status IN ('open', 'claimed')`, target, note, c.via(), s.now().Unix(), a.ID); err != nil {
		return nil, s.internal("board.done", err)
	}
	v, err = s.item(ctx, a.ID)
	if err != nil {
		return nil, err
	}
	// The author and the claimer learn that the item was closed, unless
	// they closed it themselves.
	what := "marked board item " + itoa(a.ID) + ` "` + v.UntrustedTitle + `" done.`
	if a.Withdraw {
		what = "withdrew board item " + itoa(a.ID) + ` "` + v.UntrustedTitle + `".`
	}
	if note != "" {
		what += " Note: " + note
	}
	n := notice{to: []string{v.authorAID, v.claimerAID}, text: "@" + m.Handle + " " + what}
	return map[string]any{"item": v, "notified": s.notify(ctx, m, a.ID, c.via(), n)}, nil
}

// --- board.comment ---

type commentArgs struct {
	ID   int64  `json:"id"`
	Text string `json:"text"`
}

func (s *Service) boardComment(ctx context.Context, c *call) (any, error) {
	var a commentArgs
	if err := decode(c.raw, &a); err != nil {
		return nil, err
	}
	if a.ID <= 0 {
		return nil, errMissing("id")
	}
	body, err := text("text", a.Text, maxCommentText, true)
	if err != nil {
		return nil, err
	}
	v, err := s.item(ctx, a.ID)
	if err != nil {
		return nil, err
	}
	if v.Status == statusWithdrawn {
		return nil, newErr(http.StatusConflict, CodeItemClosed, "The item was withdrawn.")
	}
	if err := s.checkQuota(ctx, c.member.AID, len(body)); err != nil {
		return nil, err
	}
	if v.CommentCount >= maxCommentsPerIt {
		return nil, newErr(http.StatusConflict, CodeLimitReached, "The item has reached the limit of %d comments.", maxCommentsPerIt)
	}
	now := s.now().Unix()
	res, err := s.db.ExecContext(ctx, `INSERT INTO board_comments(item_id, author_aid, text, created_at, via) VALUES(?, ?, ?, ?, ?)`,
		a.ID, c.member.AID, body, now, c.via())
	if err != nil {
		return nil, s.internal("board.comment", err)
	}
	id, _ := res.LastInsertId()
	if _, err := s.db.ExecContext(ctx, `UPDATE board_items SET updated_at = ? WHERE id = ?`, now, a.ID); err != nil {
		return nil, s.internal("board.comment", err)
	}
	// The author learns of comments by others: an offer of help often
	// arrives as one.
	n := notice{to: []string{v.authorAID}, text: "@" + c.member.Handle + " commented on your board item " + itoa(a.ID) +
		` "` + v.UntrustedTitle + `": ` + clip(body, maxNoticeQuote)}
	return map[string]any{"commented": true, "item_id": a.ID, "comment_id": id,
		"notified": s.notify(ctx, c.member, a.ID, c.via(), n)}, nil
}

// --- board.delete ---

type deleteArgs struct {
	ID int64 `json:"id"`
}

// boardDelete removes an item for good: only its author may. The row stays
// (ids are not reused), its text, tags and comments are erased, and it is no
// longer listed. The space counts against no quota afterwards.
func (s *Service) boardDelete(ctx context.Context, c *call) (any, error) {
	var a deleteArgs
	if err := decode(c.raw, &a); err != nil {
		return nil, err
	}
	if a.ID <= 0 {
		return nil, errMissing("id")
	}
	v, err := s.item(ctx, a.ID)
	if err != nil {
		return nil, err
	}
	if v.authorAID != c.member.AID {
		return nil, errNotPermitted("the author of the item")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, s.internal("board.delete", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `UPDATE board_items SET status = 'deleted', title = '', body = '', note = '', tags = '',
		claimer_aid = NULL, updated_at = ? WHERE id = ?`, s.now().Unix(), a.ID); err != nil {
		return nil, s.internal("board.delete", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM board_comments WHERE item_id = ?`, a.ID); err != nil {
		return nil, s.internal("board.delete", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, s.internal("board.delete", err)
	}
	n := notice{to: []string{v.claimerAID}, text: "@" + c.member.Handle + " deleted board item " + itoa(a.ID) + ` "` +
		v.UntrustedTitle + `", which you had claimed.`}
	return map[string]any{"deleted": true, "id": a.ID, "notified": s.notify(ctx, c.member, a.ID, c.via(), n)}, nil
}

// --- notifications ---

// boardSender begins the from_aid of every task board notification. It is
// not an AID (an AID never contains '#'), and its handle "board" is
// reserved, so no member can send in its name.
const boardSender = "#board"

// boardFrom is the from_aid of a notification caused by the member with the
// AID actor. Keying notifications by actor keeps one member's actions from
// using up the places of everyone else's: they count against the actor's
// own MailboxPerSender places in the recipient's mailbox (see roomFor).
func boardFrom(actor string) string { return boardSender + ":" + actor }

// maxNoticesPerItem bounds the unread notifications about one item that one
// actor's actions leave in one mailbox. Further comments stay readable on
// the item itself.
const maxNoticesPerItem = 10

// boardHandle is the sender handle that notifications are shown with.
const boardHandle = "board"

// maxNoticeQuote bounds the part of a comment that a notification quotes.
const maxNoticeQuote = 500

// notice is a notification about a board item for the members in to.
type notice struct {
	to   []string
	text string
}

// notify leaves n in the mailbox of each recipient other than the actor,
// marked with the actor's via and the item's id. A full mailbox, one in
// which the actor already holds MailboxPerSender places, or one with
// maxNoticesPerItem unread notifications by the actor about the item, is
// skipped: the action itself never fails for a notification. It returns the
// handles notified.
func (s *Service) notify(ctx context.Context, actor *Member, item int64, via string, n notice) []string {
	out := []string{}
	seen := map[string]bool{actor.AID: true, "": true}
	for _, aid := range n.to {
		if seen[aid] {
			continue
		}
		seen[aid] = true
		r, err := s.memberByAID(ctx, aid)
		if err != nil || r == nil {
			continue // a former member has no mailbox
		}
		ok, err := s.deliverNotice(ctx, r, actor, item, via, clip(n.text, maxMessageText))
		if err != nil {
			s.log.Error("board notification failed", "item", item, "err", err.Error())
			continue
		}
		if ok {
			out = append(out, r.Handle)
		}
	}
	return out
}

func (s *Service) deliverNotice(ctx context.Context, r, actor *Member, item int64, via, text string) (bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	if refusal, err := s.roomFor(ctx, tx, actor.AID, r); err != nil || refusal != nil {
		return false, err
	}
	from := boardFrom(actor.AID)
	var onItem int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM messages WHERE to_aid = ? AND from_aid = ? AND item_id = ?`,
		r.AID, from, item).Scan(&onItem); err != nil {
		return false, err
	}
	if onItem >= maxNoticesPerItem {
		return false, nil
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO messages(to_aid, from_aid, from_handle, text, sent_at, via, item_id)
		VALUES(?, ?, ?, ?, ?, ?, ?)`, r.AID, from, boardHandle, text, s.now().Unix(), via, item); err != nil {
		return false, err
	}
	return true, tx.Commit()
}

// clip shortens s to at most max bytes on a rune boundary, marking the cut.
func clip(s string, max int) string {
	if len(s) <= max {
		return s
	}
	cut := max - len(" [...]")
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + " [...]"
}
