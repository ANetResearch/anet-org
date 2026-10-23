package org

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"strings"
)

// Research limits. The research space is deliberately minimal: projects
// and notes (with a kind for reading lists and meeting minutes).
const (
	maxProjectTitle = 200
	maxProjectDesc  = 2000
	maxNoteTitle    = 200
	maxNoteText     = 12000
	maxNoteURL      = 500
	maxProjects     = 1000
	maxNotesPerProj = 5000
)

var projectNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,39}$`)

var noteKinds = map[string]bool{"note": true, "reading": true, "minutes": true}

// ProjectView is a research project.
type ProjectView struct {
	Name                 string `json:"name"`
	Owner                string `json:"owner"`
	Via                  string `json:"via,omitempty"`
	CreatedAt            string `json:"created_at"`
	NoteCount            int    `json:"note_count"`
	UntrustedTitle       string `json:"untrusted_title"`
	UntrustedDescription string `json:"untrusted_description,omitempty"`
	id                   int64
}

// NoteView is a research note.
type NoteView struct {
	ID             int64  `json:"id"`
	Project        string `json:"project"`
	Kind           string `json:"kind"`
	Author         string `json:"author"`
	Via            string `json:"via,omitempty"`
	CreatedAt      string `json:"created_at"`
	UntrustedTitle string `json:"untrusted_title,omitempty"`
	UntrustedText  string `json:"untrusted_text"`
	UntrustedURL   string `json:"untrusted_url,omitempty"`
}

const projectSelect = `SELECT p.id, p.name, COALESCE(m.handle, ''), p.created_at, p.title, p.description,
	(SELECT COUNT(*) FROM notes n WHERE n.project_id = p.id), p.via
	FROM projects p LEFT JOIN members m ON m.aid = p.owner_aid`

func scanProject(sc interface{ Scan(...any) error }) (*ProjectView, error) {
	var v ProjectView
	var at int64
	var via string
	if err := sc.Scan(&v.id, &v.Name, &v.Owner, &at, &v.UntrustedTitle, &v.UntrustedDescription, &v.NoteCount, &via); err != nil {
		return nil, err
	}
	v.Via = shownVia(via)
	if v.Owner == "" {
		v.Owner = formerMember
	}
	v.CreatedAt = ts(at)
	return &v, nil
}

func (s *Service) project(ctx context.Context, name string) (*ProjectView, error) {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		return nil, errMissing("project")
	}
	if !projectNameRe.MatchString(name) {
		return nil, newErr(http.StatusNotFound, CodeProjectUnknown, "No research project has the name %q.", name)
	}
	v, err := scanProject(s.db.QueryRowContext(ctx, projectSelect+` WHERE p.name = ?`, name))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, newErr(http.StatusNotFound, CodeProjectUnknown,
			"No research project has the name %q. List projects with research.project.list.", name)
	}
	if err != nil {
		return nil, s.internal("project", err)
	}
	return v, nil
}

type projectCreateArgs struct {
	Name        string `json:"name"`
	Title       string `json:"title"`
	Description string `json:"description"`
}

func (s *Service) projectCreate(ctx context.Context, c *call) (any, error) {
	var a projectCreateArgs
	if err := decode(c.raw, &a); err != nil {
		return nil, err
	}
	name := strings.ToLower(strings.TrimSpace(a.Name))
	if name == "" {
		return nil, errMissing("name")
	}
	if !projectNameRe.MatchString(name) {
		return nil, errInvalid("name", "a project name has 2 to 40 characters: lower-case letters, digits and '-'.")
	}
	title, err := text("title", a.Title, maxProjectTitle, false)
	if err != nil {
		return nil, err
	}
	if title == "" {
		title = name
	}
	desc, err := text("description", a.Description, maxProjectDesc, false)
	if err != nil {
		return nil, err
	}
	if err := s.checkQuota(ctx, c.member.AID, len(title)+len(desc)); err != nil {
		return nil, err
	}
	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM projects`).Scan(&n); err != nil {
		return nil, s.internal("project.create", err)
	}
	if n >= maxProjects {
		return nil, newErr(http.StatusConflict, CodeLimitReached, "The organisation has reached the limit of %d projects.", maxProjects)
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO projects(name, title, description, owner_aid, created_at, via) VALUES(?, ?, ?, ?, ?, ?)`,
		name, title, desc, c.member.AID, s.now().Unix(), c.via()); err != nil {
		if isUnique(err) {
			return nil, newErr(http.StatusConflict, CodeProjectExists, "A research project named %q exists.", name)
		}
		return nil, s.internal("project.create", err)
	}
	v, err := s.project(ctx, name)
	if err != nil {
		return nil, err
	}
	return map[string]any{"project": v}, nil
}

type projectListArgs struct {
	Query string `json:"query"`
	Limit int    `json:"limit"`
}

func (s *Service) projectList(ctx context.Context, c *call) (any, error) {
	var a projectListArgs
	if err := decode(c.raw, &a); err != nil {
		return nil, err
	}
	q, err := text("query", a.Query, 100, false)
	if err != nil {
		return nil, err
	}
	n := limit(a.Limit, 50, 200)
	rows, err := s.db.QueryContext(ctx, projectSelect+` WHERE (? = '' OR p.name LIKE ? ESCAPE '\' OR p.title LIKE ? ESCAPE '\')
		ORDER BY p.name LIMIT ?`, q, likeEscape(q), likeEscape(q), n+1)
	if err != nil {
		return nil, s.internal("project.list", err)
	}
	defer rows.Close()
	out := []*ProjectView{}
	more := false
	for rows.Next() {
		v, err := scanProject(rows)
		if err != nil {
			return nil, s.internal("project.list", err)
		}
		if len(out) == n {
			more = true
			break
		}
		out = append(out, v)
	}
	return map[string]any{"notice": untrustedNotice, "projects": out, "more": more}, rows.Err()
}

type noteAddArgs struct {
	Project string `json:"project"`
	Kind    string `json:"kind"`
	Title   string `json:"title"`
	Text    string `json:"text"`
	URL     string `json:"url"`
}

func (s *Service) noteAdd(ctx context.Context, c *call) (any, error) {
	var a noteAddArgs
	if err := decode(c.raw, &a); err != nil {
		return nil, err
	}
	p, err := s.project(ctx, a.Project)
	if err != nil {
		return nil, err
	}
	kind := strings.ToLower(strings.TrimSpace(a.Kind))
	if kind == "" {
		kind = "note"
	}
	if !noteKinds[kind] {
		return nil, errInvalid("kind", "use note, reading or minutes.")
	}
	title, err := text("title", a.Title, maxNoteTitle, false)
	if err != nil {
		return nil, err
	}
	body, err := text("text", a.Text, maxNoteText, true)
	if err != nil {
		return nil, err
	}
	link, err := text("url", a.URL, maxNoteURL, false)
	if err != nil {
		return nil, err
	}
	if link != "" {
		u, err := url.Parse(link)
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
			return nil, errInvalid("url", "use an http or https URL.")
		}
	}
	if err := s.checkQuota(ctx, c.member.AID, len(title)+len(body)+len(link)); err != nil {
		return nil, err
	}
	if p.NoteCount >= maxNotesPerProj {
		return nil, newErr(http.StatusConflict, CodeLimitReached, "The project has reached the limit of %d notes.", maxNotesPerProj)
	}
	now := s.now().Unix()
	res, err := s.db.ExecContext(ctx, `INSERT INTO notes(project_id, author_aid, kind, title, text, url, created_at, via)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?)`, p.id, c.member.AID, kind, title, body, link, now, c.via())
	if err != nil {
		return nil, s.internal("note.add", err)
	}
	id, _ := res.LastInsertId()
	return map[string]any{"note": NoteView{ID: id, Project: p.Name, Kind: kind, Author: c.member.Handle,
		Via: shownVia(c.via()), CreatedAt: ts(now), UntrustedTitle: title, UntrustedText: body, UntrustedURL: link}}, nil
}

type noteListArgs struct {
	Project string `json:"project"`
	Kind    string `json:"kind"`
	Mine    bool   `json:"mine"`
	Limit   int    `json:"limit"`
	Before  int64  `json:"before"`
}

func (s *Service) noteList(ctx context.Context, c *call) (any, error) {
	var a noteListArgs
	if err := decode(c.raw, &a); err != nil {
		return nil, err
	}
	return s.listNotes(ctx, c.member, a)
}

// listNotes lists notes of one project, or with mine and no project the
// member's own notes across projects.
func (s *Service) listNotes(ctx context.Context, m *Member, a noteListArgs) (map[string]any, error) {
	where := []string{"1 = 1"}
	args := []any{}
	if strings.TrimSpace(a.Project) != "" {
		p, err := s.project(ctx, a.Project)
		if err != nil {
			return nil, err
		}
		where = append(where, "n.project_id = ?")
		args = append(args, p.id)
	} else if !a.Mine {
		return nil, newErr(http.StatusBadRequest, CodeMissingField, `Give "project", or "mine": true for your own notes in every project.`)
	}
	if a.Kind != "" {
		if !noteKinds[a.Kind] {
			return nil, errInvalid("kind", "use note, reading or minutes.")
		}
		where = append(where, "n.kind = ?")
		args = append(args, a.Kind)
	}
	if a.Mine {
		where = append(where, "n.author_aid = ?")
		args = append(args, m.AID)
	}
	if a.Before > 0 {
		where = append(where, "n.id < ?")
		args = append(args, a.Before)
	}
	n := limit(a.Limit, 30, 100)
	args = append(args, n+1)
	rows, err := s.db.QueryContext(ctx, `SELECT n.id, p.name, n.kind, COALESCE(mm.handle, ''), n.created_at, n.title, n.text, n.url, n.via
		FROM notes n JOIN projects p ON p.id = n.project_id LEFT JOIN members mm ON mm.aid = n.author_aid
		WHERE `+strings.Join(where, " AND ")+` ORDER BY n.id DESC LIMIT ?`, args...)
	if err != nil {
		return nil, s.internal("note.list", err)
	}
	defer rows.Close()
	out := []NoteView{}
	more := false
	for rows.Next() {
		var v NoteView
		var at int64
		var via string
		if err := rows.Scan(&v.ID, &v.Project, &v.Kind, &v.Author, &at, &v.UntrustedTitle, &v.UntrustedText, &v.UntrustedURL, &via); err != nil {
			return nil, s.internal("note.list", err)
		}
		v.Via = shownVia(via)
		if len(out) == n {
			more = true
			break
		}
		if v.Author == "" {
			v.Author = formerMember
		}
		v.CreatedAt = ts(at)
		out = append(out, v)
	}
	return map[string]any{"notice": untrustedNotice, "notes": out, "more": more}, rows.Err()
}

// noteDelete deletes a note; only its author may.
func (s *Service) noteDelete(ctx context.Context, c *call) (any, error) {
	var a deleteArgs
	if err := decode(c.raw, &a); err != nil {
		return nil, err
	}
	if a.ID <= 0 {
		return nil, errMissing("id")
	}
	var author string
	err := s.db.QueryRowContext(ctx, `SELECT author_aid FROM notes WHERE id = ?`, a.ID).Scan(&author)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, newErr(http.StatusNotFound, CodeNoteUnknown, "No research note has the id %d.", a.ID)
	}
	if err != nil {
		return nil, s.internal("note.delete", err)
	}
	if author != c.member.AID {
		return nil, errNotPermitted("the author of the note")
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM notes WHERE id = ? AND author_aid = ?`, a.ID, c.member.AID); err != nil {
		return nil, s.internal("note.delete", err)
	}
	return map[string]any{"deleted": true, "id": a.ID}, nil
}
