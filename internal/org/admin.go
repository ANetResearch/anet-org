package org

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/ANetResearch/anet-org/internal/invite"
)

// Invite limits.
const (
	DefaultInviteDays = 14
	MaxInviteDays     = 90
	DefaultInviteUses = 200
	MaxInviteUses     = 10000
	maxInviteNote     = 200
	// MaxInviteBatch bounds the codes one call creates. Single-use codes,
	// one per person, are the recommended way to limit Sybil joins.
	MaxInviteBatch = 500
)

// Invite is one row of the invites table (without its hash).
type Invite struct {
	ID        int64
	Kind      string
	Note      string
	CreatedAt int64
	CreatedBy string
	ExpiresAt int64
	MaxUses   int64
	Uses      int64
	RevokedAt int64
}

func (i *Invite) usable(now int64) error {
	switch {
	case i.RevokedAt > 0:
		return newErr(http.StatusGone, CodeInviteRevoked, "The invite code was revoked. Ask an administrator for a new one.")
	case now >= i.ExpiresAt:
		return newErr(http.StatusGone, CodeInviteExpired, "The invite code expired on %s. Ask an administrator for a new one.", ts(i.ExpiresAt))
	case i.Uses >= i.MaxUses:
		return newErr(http.StatusGone, CodeInviteUsedUp, "The invite code has no uses left. Ask an administrator for a new one.")
	}
	return nil
}

// InviteView is an invite as administrators see it. The code itself is
// never stored, so it is shown only once, when it is created.
type InviteView struct {
	ID        int64  `json:"id"`
	Kind      string `json:"kind"`
	Note      string `json:"note"`
	CreatedAt string `json:"created_at"`
	CreatedBy string `json:"created_by"`
	ExpiresAt string `json:"expires_at"`
	MaxUses   int64  `json:"max_uses"`
	Uses      int64  `json:"uses"`
	RevokedAt string `json:"revoked_at,omitempty"`
	State     string `json:"state"`
}

func (s *Service) view(i *Invite) InviteView {
	st := "active"
	switch {
	case i.RevokedAt > 0:
		st = "revoked"
	case s.now().Unix() >= i.ExpiresAt:
		st = "expired"
	case i.Uses >= i.MaxUses:
		st = "used_up"
	}
	return InviteView{ID: i.ID, Kind: i.Kind, Note: i.Note, CreatedAt: ts(i.CreatedAt), CreatedBy: i.CreatedBy,
		ExpiresAt: ts(i.ExpiresAt), MaxUses: i.MaxUses, Uses: i.Uses, RevokedAt: ts(i.RevokedAt), State: st}
}

const inviteCols = `id, kind, note, created_at, created_by, expires_at, max_uses, uses, COALESCE(revoked_at, 0)`

func scanInvite(sc interface{ Scan(...any) error }) (*Invite, error) {
	var i Invite
	if err := sc.Scan(&i.ID, &i.Kind, &i.Note, &i.CreatedAt, &i.CreatedBy, &i.ExpiresAt, &i.MaxUses, &i.Uses, &i.RevokedAt); err != nil {
		return nil, err
	}
	return &i, nil
}

func errInviteUnknown() *Error {
	return newErr(http.StatusNotFound, CodeInviteUnknown, "The invite code is not known to this organisation. Check the code and the organisation.")
}

// findInvite parses a typed code and finds its invite. The lookup key
// selects the row; the full digest is then compared in constant time.
func (s *Service) findInvite(ctx context.Context, code string) (*Invite, error) {
	body, err := invite.Parse(s.cfg.Org.Prefix, code)
	if err != nil {
		return nil, errInviteUnknown()
	}
	d := invite.Hash(s.cfg.Org.Prefix, body)
	var hash []byte
	row := s.db.QueryRowContext(ctx, `SELECT hash, `+inviteCols+` FROM invites WHERE lookup = ?`, d.Lookup)
	var i Invite
	err = row.Scan(&hash, &i.ID, &i.Kind, &i.Note, &i.CreatedAt, &i.CreatedBy, &i.ExpiresAt, &i.MaxUses, &i.Uses, &i.RevokedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, errInviteUnknown()
	}
	if err != nil {
		return nil, s.internal("invite", err)
	}
	if !invite.Equal(hash, d.Hash) {
		return nil, errInviteUnknown()
	}
	return &i, nil
}

// InviteOptions are the parameters of a new invite.
type InviteOptions struct {
	// Count is the number of codes to create (1 when 0), each with the same
	// settings; at most MaxInviteBatch.
	Count       int    `json:"count"`
	ExpiresDays int    `json:"expires_days"`
	Uses        int    `json:"uses"`
	Kind        string `json:"kind"`
	Note        string `json:"note"`
}

// CreateInvites makes o.Count codes (one when 0) with the same settings, in
// one transaction-free batch; on an error the codes made so far stay valid
// and are returned with it.
func (s *Service) CreateInvites(ctx context.Context, by string, o InviteOptions) ([]string, []InviteView, error) {
	n := o.Count
	if n == 0 {
		n = 1
	}
	if n < 1 || n > MaxInviteBatch {
		return nil, nil, errInvalid("count", "use 1 to 500.")
	}
	o.Count = 1
	var codes []string
	var views []InviteView
	for i := 0; i < n; i++ {
		c, v, err := s.CreateInvite(ctx, by, o)
		if err != nil {
			return codes, views, err
		}
		codes = append(codes, c)
		views = append(views, v)
	}
	return codes, views, nil
}

// CreateInvite makes a new code and returns it with its record. by names
// the administrator (an AID, or "operator" for the command line).
func (s *Service) CreateInvite(ctx context.Context, by string, o InviteOptions) (string, InviteView, error) {
	if o.Count > 1 {
		return "", InviteView{}, errInvalid("count", "use CreateInvites for more than one code.")
	}
	if o.ExpiresDays == 0 {
		o.ExpiresDays = DefaultInviteDays
	}
	if o.ExpiresDays < 1 || o.ExpiresDays > MaxInviteDays {
		return "", InviteView{}, errInvalid("expires_days", "use 1 to 90 days.")
	}
	if o.Uses == 0 {
		o.Uses = DefaultInviteUses
	}
	if o.Uses < 1 || o.Uses > MaxInviteUses {
		return "", InviteView{}, errInvalid("uses", "use 1 to 10000.")
	}
	if o.Kind == "" {
		o.Kind = KindStudent
	}
	if !validKind(o.Kind) {
		return "", InviteView{}, errInvalid("kind", "use student, service or staff.")
	}
	note, err := text("note", o.Note, maxInviteNote, false)
	if err != nil {
		return "", InviteView{}, err
	}
	now := s.now().Unix()
	exp := now + int64(o.ExpiresDays)*86400
	for attempt := 0; attempt < 5; attempt++ {
		code, err := invite.New(s.cfg.Org.Prefix)
		if err != nil {
			return "", InviteView{}, s.internal("invite.create", err)
		}
		body, _ := invite.Parse(s.cfg.Org.Prefix, code)
		d := invite.Hash(s.cfg.Org.Prefix, body)
		res, err := s.db.ExecContext(ctx, `INSERT INTO invites(lookup, hash, kind, note, created_at, created_by, expires_at, max_uses)
			VALUES(?, ?, ?, ?, ?, ?, ?, ?)`, d.Lookup, d.Hash, o.Kind, note, now, by, exp, o.Uses)
		if isUnique(err) {
			continue
		}
		if err != nil {
			return "", InviteView{}, s.internal("invite.create", err)
		}
		id, _ := res.LastInsertId()
		s.log.Info("invite created", "invite", id, "by", by, "kind", o.Kind, "uses", o.Uses, "days", o.ExpiresDays)
		return code, s.view(&Invite{ID: id, Kind: o.Kind, Note: note, CreatedAt: now, CreatedBy: by, ExpiresAt: exp, MaxUses: int64(o.Uses)}), nil
	}
	return "", InviteView{}, s.internal("invite.create", errors.New("no free code after 5 attempts"))
}

// RevokeInvite revokes an invite. When removeAfter is set, members who
// joined with it at or after that time are removed too; the count is
// returned.
func (s *Service) RevokeInvite(ctx context.Context, by string, id int64, removeAfter *time.Time) (int, error) {
	res, err := s.db.ExecContext(ctx, `UPDATE invites SET revoked_at = ? WHERE id = ? AND revoked_at IS NULL`, s.now().Unix(), id)
	if err != nil {
		return 0, s.internal("invite.revoke", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		var exists int
		if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM invites WHERE id = ?`, id).Scan(&exists); err != nil {
			return 0, s.internal("invite.revoke", err)
		}
		if exists == 0 {
			return 0, newErr(http.StatusNotFound, CodeInviteUnknown, "No invite has the id %d.", id)
		}
	}
	removed := 0
	if removeAfter != nil {
		rows, err := s.db.QueryContext(ctx, `SELECT aid FROM members WHERE invite_id = ? AND joined_at >= ?`, id, removeAfter.Unix())
		if err != nil {
			return 0, s.internal("invite.revoke", err)
		}
		var aids []string
		for rows.Next() {
			var a string
			if err := rows.Scan(&a); err != nil {
				rows.Close()
				return 0, s.internal("invite.revoke", err)
			}
			aids = append(aids, a)
		}
		rows.Close()
		for _, a := range aids {
			m, err := s.memberByAID(ctx, a)
			if err != nil {
				return removed, err
			}
			if m == nil {
				continue
			}
			if err := s.removeMember(ctx, m, false); err != nil {
				return removed, err
			}
			removed++
		}
	}
	s.log.Info("invite revoked", "invite", id, "by", by, "removed_members", removed)
	return removed, nil
}

// ListInvites lists invites, newest first; inactive ones only when all.
func (s *Service) ListInvites(ctx context.Context, all bool) ([]InviteView, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+inviteCols+` FROM invites ORDER BY id DESC LIMIT 1000`)
	if err != nil {
		return nil, s.internal("invite.list", err)
	}
	defer rows.Close()
	out := []InviteView{}
	for rows.Next() {
		i, err := scanInvite(rows)
		if err != nil {
			return nil, s.internal("invite.list", err)
		}
		v := s.view(i)
		if !all && v.State != "active" {
			continue
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// RemoveMember removes a member by handle, optionally banning its AID from
// joining again.
func (s *Service) RemoveMember(ctx context.Context, by, handle string, ban bool) (*Member, error) {
	h := normalizeHandle(handle)
	if h == "" {
		return nil, errMissing("handle")
	}
	m, err := s.memberByHandle(ctx, h)
	if err != nil {
		return nil, err
	}
	if m == nil {
		return nil, newErr(http.StatusNotFound, CodeMemberUnknown, "No member has the handle %q.", h)
	}
	if err := s.removeMember(ctx, m, false); err != nil {
		return nil, err
	}
	if ban {
		if _, err := s.db.ExecContext(ctx, `INSERT INTO bans(aid, at, by) VALUES(?, ?, ?) ON CONFLICT(aid) DO NOTHING`,
			m.AID, s.now().Unix(), by); err != nil {
			return nil, s.internal("member.remove", err)
		}
	}
	s.log.Info("member removed", "aid", m.AID, "by", by, "ban", ban)
	return m, nil
}

// MemberRecord is a member as the operator sees it.
type MemberRecord struct {
	Handle   string `json:"handle"`
	Kind     string `json:"kind"`
	AID      string `json:"aid"`
	JoinedAt string `json:"joined_at"`
	InviteID int64  `json:"invite_id,omitempty"`
}

// ListMembers lists members for the operator, with AIDs.
func (s *Service) ListMembers(ctx context.Context, query string) ([]MemberRecord, error) {
	q := normalizeHandle(query)
	rows, err := s.db.QueryContext(ctx, `SELECT aid, handle, kind, joined_at, COALESCE(invite_id, 0) FROM members
		WHERE (? = '' OR handle LIKE ? ESCAPE '\') ORDER BY handle`, q, likeEscape(q))
	if err != nil {
		return nil, s.internal("members", err)
	}
	defer rows.Close()
	out := []MemberRecord{}
	for rows.Next() {
		var m Member
		var inv int64
		if err := rows.Scan(&m.AID, &m.Handle, &m.Kind, &m.JoinedAt, &inv); err != nil {
			return nil, s.internal("members", err)
		}
		out = append(out, MemberRecord{Handle: m.Handle, Kind: s.kindOf(&m), AID: m.AID, JoinedAt: ts(m.JoinedAt), InviteID: inv})
	}
	return out, rows.Err()
}

// SetKind changes a member's stored kind (operator command line).
func (s *Service) SetKind(ctx context.Context, handle, kind string) error {
	if !validKind(kind) {
		return errInvalid("kind", "use student, service or staff.")
	}
	res, err := s.db.ExecContext(ctx, `UPDATE members SET kind = ? WHERE handle = ?`, kind, normalizeHandle(handle))
	if err != nil {
		return s.internal("member.kind", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return newErr(http.StatusNotFound, CodeMemberUnknown, "No member has the handle %q.", normalizeHandle(handle))
	}
	return nil
}

func adminName(c *call) string {
	if c.caller.Local {
		return "operator"
	}
	return c.caller.AID
}

// --- admin.* capabilities ---

func (s *Service) adminInviteCreate(ctx context.Context, c *call) (any, error) {
	var a InviteOptions
	if err := decode(c.raw, &a); err != nil {
		return nil, err
	}
	codes, vs, err := s.CreateInvites(ctx, adminName(c), a)
	if err != nil && len(codes) == 0 {
		return nil, err
	}
	out := map[string]any{"codes": codes, "invites": vs,
		"notice": "Each code is shown only once; only its hash is stored. Give each person their own single-use code where possible."}
	if len(codes) == 1 {
		out["code"] = codes[0]
		out["invite"] = vs[0]
	}
	if err != nil {
		out["incomplete"] = true
	}
	return out, nil
}

type revokeArgs struct {
	ID                int64  `json:"id"`
	RemoveJoinedAfter string `json:"remove_joined_after"`
}

func (s *Service) adminInviteRevoke(ctx context.Context, c *call) (any, error) {
	var a revokeArgs
	if err := decode(c.raw, &a); err != nil {
		return nil, err
	}
	if a.ID <= 0 {
		return nil, errMissing("id")
	}
	var after *time.Time
	if v := strings.TrimSpace(a.RemoveJoinedAfter); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			return nil, errInvalid("remove_joined_after", "use an RFC 3339 time such as 2026-10-21T08:00:00Z.")
		}
		after = &t
	}
	n, err := s.RevokeInvite(ctx, adminName(c), a.ID, after)
	if err != nil {
		return nil, err
	}
	return map[string]any{"revoked": true, "id": a.ID, "removed_members": n}, nil
}

type inviteListArgs struct {
	All bool `json:"all"`
}

func (s *Service) adminInviteList(ctx context.Context, c *call) (any, error) {
	var a inviteListArgs
	if err := decode(c.raw, &a); err != nil {
		return nil, err
	}
	v, err := s.ListInvites(ctx, a.All)
	if err != nil {
		return nil, err
	}
	return map[string]any{"invites": v}, nil
}

type memberRemoveArgs struct {
	Handle string `json:"handle"`
	Ban    bool   `json:"ban"`
}

func (s *Service) adminMemberRemove(ctx context.Context, c *call) (any, error) {
	var a memberRemoveArgs
	if err := decode(c.raw, &a); err != nil {
		return nil, err
	}
	m, err := s.RemoveMember(ctx, adminName(c), a.Handle, a.Ban)
	if err != nil {
		return nil, err
	}
	return map[string]any{"removed": m.Handle, "banned": a.Ban}, nil
}
