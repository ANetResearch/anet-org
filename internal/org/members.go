package org

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/ANetResearch/anet-org/internal/invite"
)

// Member kinds.
const (
	KindStudent = "student"
	KindService = "service"
	KindStaff   = "staff"
)

func validKind(k string) bool { return k == KindStudent || k == KindService || k == KindStaff }

// Member is one row of the members table.
type Member struct {
	AID       string
	Handle    string
	Kind      string
	JoinedAt  int64
	InviteID  sql.NullInt64
	RenamedAt int64
	Dropped   int64
}

// RenameInterval is the least time between two renames.
const RenameInterval = 7 * 24 * time.Hour

// HandleHold is how long a handle given up by leaving or renaming stays
// reserved for its former holder, so nobody else can take it at once and
// be mistaken for them.
const HandleHold = 30 * 24 * time.Hour

var handleRe = regexp.MustCompile(`^[a-z][a-z0-9_-]{1,23}$`)

// reservedHandles cannot be taken by anyone. Names that suggest authority
// or a function of the organisation agent are on it, and so are the group
// names msg.send understands.
var reservedHandles = map[string]bool{
	"admin": true, "admins": true, "administrator": true, "staff": true, "helpdesk": true, "help": true,
	"support": true, "taskboard": true, "board": true, "research": true, "anet": true, "system": true,
	"root": true, "org": true, "all": true, "everyone": true, "here": true, "me": true, "moderator": true,
	"mod": true, "mods": true, "official": true, "owner": true, "security": true, "null": true, "nobody": true,
	"assistant": true, "ask": true, "bot": true, "service": true, "operator": true, "postmaster": true,
	"abuse": true, "info": true, "noreply": true, "no-reply": true, "webmaster": true, "teacher": true,
	"professor": true, "president": true, "dean": true, "office": true, "announce": true, "announcements": true,
	"members": true, "inbox": true, "hub": true, "agent": true, "daemon": true, "anonymous": true,
}

// normalizeHandle lower-cases a handle and drops one leading '@'.
func normalizeHandle(h string) string {
	h = strings.TrimSpace(h)
	h = strings.TrimPrefix(h, "@")
	return strings.ToLower(h)
}

// checkHandle validates a handle against the syntax and the reserved list.
func (s *Service) checkHandle(raw string) (string, error) {
	if len(raw) > 64 {
		return "", newErr(http.StatusBadRequest, CodeInvalidHandle, "A handle has 2 to 24 characters.")
	}
	h := normalizeHandle(raw)
	if h == "" {
		return "", errMissing("handle")
	}
	if !handleRe.MatchString(h) {
		return "", newErr(http.StatusBadRequest, CodeInvalidHandle,
			"A handle has 2 to 24 characters: lower-case letters, digits, '_' and '-', starting with a letter.")
	}
	if s.reserved(h) {
		return "", newErr(http.StatusConflict, CodeHandleReserved, "The handle %q is reserved.", h)
	}
	return h, nil
}

// Skeleton folds the characters that look alike in a handle (1, l and i to
// i; 0 and o to o; 5 and s to s) and drops the separators '-' and '_'. Two
// handles with the same skeleton cannot be held by different members, so
// "a1ice" or "alice_" cannot stand in for "alice".
func Skeleton(h string) string {
	var b strings.Builder
	for i := 0; i < len(h); i++ {
		c := h[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		switch c {
		case '-', '_':
			continue
		case '1', 'l':
			c = 'i'
		case '0':
			c = 'o'
		case '5':
			c = 's'
		}
		b.WriteByte(c)
	}
	return b.String()
}

func (s *Service) reserved(h string) bool {
	sk := Skeleton(h)
	if reservedHandles[h] || reservedSkeletons[sk] {
		return true
	}
	if sk == Skeleton(s.cfg.Org.Prefix) || sk == Skeleton(strings.Join(strings.Fields(s.cfg.Org.Name), "-")) {
		return true
	}
	// The first part of a separated handle may not be the prefix or anet:
	// "lab-office", "1ab_news" and "anet-bot" are reserved, "label" is not.
	if i := strings.IndexAny(h, "-_"); i > 0 {
		first := Skeleton(h[:i])
		if first == Skeleton(s.cfg.Org.Prefix) || first == Skeleton("anet") {
			return true
		}
	}
	for _, w := range []string{"admin", "staff", "official", "moderator"} {
		if strings.HasPrefix(sk, Skeleton(w)) {
			return true
		}
	}
	return false
}

// reservedSkeletons are the skeletons of the reserved handles.
var reservedSkeletons = func() map[string]bool {
	m := map[string]bool{}
	for h := range reservedHandles {
		m[Skeleton(h)] = true
	}
	return m
}()

// handleFree reports whether h may be taken by aid: no other member holds h
// or a handle with the same skeleton, and no former holder other than aid
// has such a handle on hold.
func handleFree(ctx context.Context, tx *sql.Tx, h, aid string, now int64) (bool, error) {
	sk := Skeleton(h)
	var n int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM members WHERE (handle = ? OR skeleton = ?) AND aid <> ?`,
		h, sk, aid).Scan(&n); err != nil {
		return false, err
	}
	if n > 0 {
		return false, nil
	}
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM held_handles WHERE (handle = ? OR skeleton = ?) AND until > ? AND aid <> ?`,
		h, sk, now, aid).Scan(&n); err != nil {
		return false, err
	}
	return n == 0, nil
}

// fillSkeletons computes the skeleton of rows written before it existed.
func (s *Service) fillSkeletons(ctx context.Context) error {
	for _, t := range []string{"members", "held_handles"} {
		rows, err := s.db.QueryContext(ctx, `SELECT handle FROM `+t+` WHERE skeleton = ''`)
		if err != nil {
			return err
		}
		var hs []string
		for rows.Next() {
			var h string
			if err := rows.Scan(&h); err != nil {
				rows.Close()
				return err
			}
			hs = append(hs, h)
		}
		rows.Close()
		for _, h := range hs {
			if _, err := s.db.ExecContext(ctx, `UPDATE `+t+` SET skeleton = ? WHERE handle = ?`, Skeleton(h), h); err != nil {
				return err
			}
		}
	}
	return nil
}

func errHandleTaken(h string) *Error {
	return newErr(http.StatusConflict, CodeHandleTaken, "The handle %q is taken. Choose another one.", h)
}

func (s *Service) memberByAID(ctx context.Context, aid string) (*Member, error) {
	return s.scanMember(s.db.QueryRowContext(ctx, `SELECT aid, handle, kind, joined_at, invite_id,
		COALESCE(renamed_at, 0), mailbox_dropped FROM members WHERE aid = ?`, aid))
}

func (s *Service) memberByHandle(ctx context.Context, h string) (*Member, error) {
	return s.scanMember(s.db.QueryRowContext(ctx, `SELECT aid, handle, kind, joined_at, invite_id,
		COALESCE(renamed_at, 0), mailbox_dropped FROM members WHERE handle = ?`, h))
}

func (s *Service) scanMember(row *sql.Row) (*Member, error) {
	var m Member
	err := row.Scan(&m.AID, &m.Handle, &m.Kind, &m.JoinedAt, &m.InviteID, &m.RenamedAt, &m.Dropped)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, s.internal("member", err)
	}
	return &m, nil
}

// kindOf is the kind a member is shown with.
func (s *Service) kindOf(m *Member) string {
	if s.isStaff(m) {
		return KindStaff
	}
	return m.Kind
}

// --- org.info ---

func (s *Service) orgInfo(ctx context.Context, c *call) (any, error) {
	var a struct{}
	if err := decode(c.raw, &a); err != nil {
		return nil, err
	}
	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM members`).Scan(&n); err != nil {
		return nil, s.internal("org.info", err)
	}
	how := s.cfg.Org.JoinInstructions
	if how == "" {
		how = "Ask an administrator of " + s.cfg.Org.Name + " for an invite code, then call org.join with " +
			`{"code": "` + s.cfg.Org.Prefix + `-XXXX-XXXX", "handle": "<your handle>"}.`
	}
	you := map[string]any{"member": c.member != nil}
	if c.member != nil {
		you["handle"] = c.member.Handle
		you["kind"] = s.kindOf(c.member)
	}
	if c.caller.AID != "" && s.admins[c.caller.AID] {
		you["admin"] = true
	}
	return map[string]any{
		"name":          s.cfg.Org.Name,
		"prefix":        s.cfg.Org.Prefix,
		"description":   s.cfg.Org.Description,
		"contact":       s.cfg.Org.Contact,
		"how_to_join":   how,
		"invite_format": s.cfg.Org.Prefix + "-XXXX-XXXX",
		"stats":         map[string]any{"members": n},
		"caller":        you,
		"capabilities":  s.PublicCapabilities(),
	}, nil
}

// --- org.admit ---

// orgAdmit answers the node's admission question for a text task: whether
// the caller is an active member. The node calls it with the peer as
// X-ANet-Caller (inbound.admit) and admits on {"admit": true} only. The
// arguments are ignored, and the reply says nothing but yes or no, so the
// capability reveals no more than org.info already tells any caller about
// itself. It is internal to the node and never listed on the card.
func (s *Service) orgAdmit(_ context.Context, c *call) (any, error) {
	return map[string]bool{"admit": c.member != nil}, nil
}

// --- org.join ---

type joinArgs struct {
	Code   string `json:"code"`
	Handle string `json:"handle"`
}

// failedJoinWindow is the window of the failed-join limits.
const failedJoinWindow = 10 * time.Minute

func (s *Service) orgJoin(ctx context.Context, c *call) (any, error) {
	// Failed attempts are bounded per caller and for everyone, so guessing
	// codes is slow; the per-code limit counts only successful joins, so
	// failures with a known code cannot lock that code for others.
	if over, retry := s.lim.Over("joinfail|"+c.caller.AID, failedJoinWindow, s.cfg.FailedJoinsPerCaller); over {
		return nil, errRate(retry)
	}
	if over, retry := s.lim.Over("joinfail|*", failedJoinWindow, s.cfg.FailedJoinsGlobal); over {
		return nil, errRate(retry)
	}
	res, err := s.join(ctx, c)
	if err != nil {
		if e := AsError(err); e == nil || (e.Code != CodeRateLimited && e.Code != CodeInternal) {
			s.lim.Add("joinfail|"+c.caller.AID, failedJoinWindow)
			s.lim.Add("joinfail|*", failedJoinWindow)
		}
		return nil, err
	}
	return res, nil
}

func (s *Service) join(ctx context.Context, c *call) (any, error) {
	var a joinArgs
	if err := decode(c.raw, &a); err != nil {
		return nil, err
	}
	if strings.TrimSpace(a.Code) == "" {
		return nil, errMissing("code")
	}
	if len(a.Code) > invite.MaxInputLen {
		return nil, errTooLong("code", invite.MaxInputLen)
	}
	h, err := s.checkHandle(a.Handle)
	if err != nil {
		return nil, err
	}
	if c.member != nil {
		return nil, newErr(http.StatusConflict, CodeAlreadyMember, "The caller is already a member, with the handle %q.", c.member.Handle)
	}
	var banned int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM bans WHERE aid = ?`, c.caller.AID).Scan(&banned); err != nil {
		return nil, s.internal("join", err)
	}
	if banned > 0 {
		return nil, newErr(http.StatusForbidden, CodeJoinRefused, "This identity cannot join the organisation. Contact an administrator.")
	}
	inv, err := s.findInvite(ctx, a.Code)
	if err != nil {
		return nil, err
	}
	now := s.now().Unix()
	if err := inv.usable(now); err != nil {
		return nil, err
	}
	codeKey := "code|" + itoa(inv.ID)
	if over, retry := s.lim.Over(codeKey, time.Minute, s.cfg.JoinsPerMinutePerCode); over {
		return nil, errRate(retry)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, s.internal("join", err)
	}
	defer tx.Rollback()
	free, err := handleFree(ctx, tx, h, c.caller.AID, now)
	if err != nil {
		return nil, s.internal("join", err)
	}
	if !free {
		return nil, errHandleTaken(h)
	}
	res, err := tx.ExecContext(ctx, `UPDATE invites SET uses = uses + 1
		WHERE id = ? AND uses < max_uses AND revoked_at IS NULL AND expires_at > ?`, inv.ID, now)
	if err != nil {
		return nil, s.internal("join", err)
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return nil, newErr(http.StatusGone, CodeInviteUsedUp, "The invite code has no uses left.")
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO members(aid, handle, skeleton, kind, joined_at, invite_id) VALUES(?, ?, ?, ?, ?, ?)`,
		c.caller.AID, h, Skeleton(h), inv.Kind, now, inv.ID); err != nil {
		if isUnique(err) {
			return nil, errHandleTaken(h)
		}
		return nil, s.internal("join", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM held_handles WHERE aid = ?`, c.caller.AID); err != nil {
		return nil, s.internal("join", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, s.internal("join", err)
	}
	s.lim.Add(codeKey, time.Minute)
	s.log.Info("member joined", "aid", c.caller.AID, "invite", inv.ID)
	return map[string]any{
		"joined": true,
		"org":    s.cfg.Org.Name,
		"handle": h,
		"kind":   inv.Kind,
		"next_steps": "Welcome to " + s.cfg.Org.Name + ". Read your messages with msg.inbox, see open tasks with " +
			"board.list, and list members with org.members.",
	}, nil
}

func isUnique(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
}

// --- org.me ---

func (s *Service) orgMe(ctx context.Context, c *call) (any, error) {
	var a struct{}
	if err := decode(c.raw, &a); err != nil {
		return nil, err
	}
	m := c.member
	var unread int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM messages WHERE to_aid = ?`, m.AID).Scan(&unread); err != nil {
		return nil, s.internal("org.me", err)
	}
	next := ""
	if m.RenamedAt > 0 {
		next = ts(m.RenamedAt + int64(RenameInterval/time.Second))
	}
	return map[string]any{
		"handle":         m.Handle,
		"kind":           s.kindOf(m),
		"aid":            m.AID,
		"joined_at":      ts(m.JoinedAt),
		"renamed_at":     ts(m.RenamedAt),
		"next_rename_at": next,
		"admin":          s.admins[m.AID],
		"staff":          s.isStaff(m),
		"mailbox":        map[string]any{"messages": unread, "limit": s.cfg.MailboxMax},
	}, nil
}

// --- org.leave ---

type leaveArgs struct {
	Confirm bool `json:"confirm"`
}

func (s *Service) orgLeave(ctx context.Context, c *call) (any, error) {
	var a leaveArgs
	if err := decode(c.raw, &a); err != nil {
		return nil, err
	}
	if !a.Confirm {
		return nil, newErr(http.StatusBadRequest, CodeConfirmRequired,
			`Leaving deletes the membership and the mailbox. Call again with {"confirm": true}.`)
	}
	if err := s.removeMember(ctx, c.member, true); err != nil {
		return nil, err
	}
	s.log.Info("member left", "aid", c.member.AID)
	return map[string]any{"left": true, "handle": c.member.Handle,
		"note": "The membership and the mailbox were deleted. Joining again needs a new invite code."}, nil
}

// removeMember deletes a member and its mailbox and reopens the items it
// had claimed. With hold (a member leaving), its handle stays reserved for
// it for HandleHold; without (an administrator's removal), the handle and
// any hold of the member are released at once.
func (s *Service) removeMember(ctx context.Context, m *Member, hold bool) error {
	now := s.now().Unix()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return s.internal("remove", err)
	}
	defer tx.Rollback()
	stmts := []struct {
		q    string
		args []any
	}{
		{`DELETE FROM members WHERE aid = ?`, []any{m.AID}},
		{`DELETE FROM messages WHERE to_aid = ?`, []any{m.AID}},
		{`UPDATE board_items SET status = 'open', claimer_aid = NULL, updated_at = ? WHERE claimer_aid = ? AND status = 'claimed'`, []any{now, m.AID}},
	}
	if hold {
		stmts = append(stmts, struct {
			q    string
			args []any
		}{`INSERT INTO held_handles(handle, skeleton, aid, until) VALUES(?, ?, ?, ?)
		  ON CONFLICT(handle) DO UPDATE SET aid = excluded.aid, until = excluded.until, skeleton = excluded.skeleton`,
			[]any{m.Handle, Skeleton(m.Handle), m.AID, now + int64(HandleHold/time.Second)}})
	} else {
		stmts = append(stmts, struct {
			q    string
			args []any
		}{`DELETE FROM held_handles WHERE aid = ?`, []any{m.AID}})
	}
	for _, st := range stmts {
		if _, err := tx.ExecContext(ctx, st.q, st.args...); err != nil {
			return s.internal("remove", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return s.internal("remove", err)
	}
	return nil
}

// --- org.rename ---

type renameArgs struct {
	Handle string `json:"handle"`
}

func (s *Service) orgRename(ctx context.Context, c *call) (any, error) {
	var a renameArgs
	if err := decode(c.raw, &a); err != nil {
		return nil, err
	}
	h, err := s.checkHandle(a.Handle)
	if err != nil {
		return nil, err
	}
	m := c.member
	if h == m.Handle {
		return nil, errInvalid("handle", "it is the current handle.")
	}
	now := s.now().Unix()
	if m.RenamedAt > 0 {
		next := m.RenamedAt + int64(RenameInterval/time.Second)
		if now < next {
			return nil, newErr(http.StatusConflict, CodeRenameTooSoon,
				"A handle can be changed once every 7 days. The next change is possible after %s.", ts(next))
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, s.internal("rename", err)
	}
	defer tx.Rollback()
	free, err := handleFree(ctx, tx, h, m.AID, now)
	if err != nil {
		return nil, s.internal("rename", err)
	}
	if !free {
		return nil, errHandleTaken(h)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE members SET handle = ?, skeleton = ?, renamed_at = ? WHERE aid = ?`, h, Skeleton(h), now, m.AID); err != nil {
		if isUnique(err) {
			return nil, errHandleTaken(h)
		}
		return nil, s.internal("rename", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO held_handles(handle, skeleton, aid, until) VALUES(?, ?, ?, ?)
		ON CONFLICT(handle) DO UPDATE SET aid = excluded.aid, until = excluded.until, skeleton = excluded.skeleton`,
		m.Handle, Skeleton(m.Handle), m.AID, now+int64(HandleHold/time.Second)); err != nil {
		return nil, s.internal("rename", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM held_handles WHERE handle = ? AND aid = ?`, h, m.AID); err != nil {
		return nil, s.internal("rename", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, s.internal("rename", err)
	}
	return map[string]any{"handle": h, "previous": m.Handle,
		"next_rename_at": ts(now + int64(RenameInterval/time.Second))}, nil
}

// --- org.members ---

type membersArgs struct {
	Query      string `json:"query"`
	Kind       string `json:"kind"`
	IncludeAID bool   `json:"include_aid"`
	Limit      int    `json:"limit"`
	Offset     int    `json:"offset"`
}

// MemberView is a member as other members see it.
type MemberView struct {
	Handle   string `json:"handle"`
	Kind     string `json:"kind"`
	JoinedAt string `json:"joined_at"`
	AID      string `json:"aid,omitempty"`
}

func (s *Service) orgMembers(ctx context.Context, c *call) (any, error) {
	var a membersArgs
	if err := decode(c.raw, &a); err != nil {
		return nil, err
	}
	return s.listMembers(ctx, a)
}

func (s *Service) listMembers(ctx context.Context, a membersArgs) (map[string]any, error) {
	q, err := text("query", a.Query, 64, false)
	if err != nil {
		return nil, err
	}
	if a.Kind != "" && !validKind(a.Kind) {
		return nil, errInvalid("kind", "use student, service or staff.")
	}
	if a.Offset < 0 || a.Offset > 100000 {
		return nil, errInvalid("offset", "use a number from 0 to 100000.")
	}
	n := limit(a.Limit, 100, 500)
	rows, err := s.db.QueryContext(ctx, `SELECT aid, handle, kind, joined_at FROM members
		WHERE (? = '' OR handle LIKE ? ESCAPE '\') ORDER BY handle LIMIT ? OFFSET ?`,
		q, likeEscape(strings.ToLower(normalizeHandle(q))), n+1+5000, a.Offset)
	if err != nil {
		return nil, s.internal("members", err)
	}
	defer rows.Close()
	out := []MemberView{}
	more := false
	for rows.Next() {
		var m Member
		if err := rows.Scan(&m.AID, &m.Handle, &m.Kind, &m.JoinedAt); err != nil {
			return nil, s.internal("members", err)
		}
		k := s.kindOf(&m)
		if a.Kind != "" && k != a.Kind {
			continue
		}
		if len(out) == n {
			more = true
			break
		}
		v := MemberView{Handle: m.Handle, Kind: k, JoinedAt: ts(m.JoinedAt)}
		if a.IncludeAID {
			v.AID = m.AID
		}
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		return nil, s.internal("members", err)
	}
	return map[string]any{"members": out, "more": more}, nil
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }
