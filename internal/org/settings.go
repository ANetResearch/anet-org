package org

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
)

// AgentSettings are the settings of the organisation's agent that the
// administrators manage: a brief the agent follows in every run (its
// persona, scope and rules) and the model tier it uses.
type AgentSettings struct {
	Brief     string `json:"brief"`
	Model     string `json:"model"`
	UpdatedAt string `json:"updated_at,omitempty"`
	UpdatedBy string `json:"updated_by,omitempty"`
	// OpenJoinPaused is set (and shown) only in an organisation with
	// open_join: joining without a code is paused.
	OpenJoinPaused *bool `json:"open_join_paused,omitempty"`
}

// The model tiers: "plan" is the subscription endpoint, "api" the metered one.
const (
	ModelPlan = "plan"
	ModelAPI  = "api"
)

// MaxBrief bounds the agent brief.
const MaxBrief = 8000

var settingsKeys = []string{"agent.brief", "agent.model", "agent.updated_at", "agent.updated_by"}

// Settings reads the agent settings; a database without them has the defaults.
func (s *Service) Settings(ctx context.Context) (AgentSettings, error) {
	v := map[string]string{}
	for _, k := range settingsKeys {
		var val string
		err := s.db.QueryRowContext(ctx, `SELECT value FROM meta WHERE key = ?`, k).Scan(&val)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return AgentSettings{}, s.internal("settings", err)
		}
		v[k] = val
	}
	out := AgentSettings{Brief: v["agent.brief"], Model: v["agent.model"], UpdatedBy: v["agent.updated_by"]}
	if out.Model == "" {
		out.Model = ModelPlan
	}
	if n, err := strconv.ParseInt(v["agent.updated_at"], 10, 64); err == nil {
		out.UpdatedAt = ts(n)
	}
	if s.cfg.OpenJoin.Enabled {
		p, err := s.openJoinPaused(ctx)
		if err != nil {
			return AgentSettings{}, err
		}
		out.OpenJoinPaused = &p
	}
	return out, nil
}

// SetOpenJoinPaused pauses or resumes joining without a code.
func (s *Service) SetOpenJoinPaused(ctx context.Context, by string, paused bool) error {
	if !s.cfg.OpenJoin.Enabled {
		return errInvalid("open_join_paused", "this organisation does not admit members without a code.")
	}
	v := "0"
	if paused {
		v = "1"
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO meta(key, value) VALUES('open_join.paused', ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`, v); err != nil {
		return s.internal("settings", err)
	}
	s.log.Info("open join paused", "paused", paused, "by", by)
	return nil
}

// SetSettings stores the agent settings and rewrites the profile file.
func (s *Service) SetSettings(ctx context.Context, by string, brief, model *string) (AgentSettings, error) {
	cur, err := s.Settings(ctx)
	if err != nil {
		return AgentSettings{}, err
	}
	if brief != nil {
		b, err := text("brief", *brief, MaxBrief, false)
		if err != nil {
			return AgentSettings{}, err
		}
		cur.Brief = b
	}
	if model != nil {
		if *model != ModelPlan && *model != ModelAPI {
			return AgentSettings{}, errInvalid("model", "use plan or api.")
		}
		cur.Model = *model
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return AgentSettings{}, s.internal("settings", err)
	}
	defer tx.Rollback()
	vals := map[string]string{"agent.brief": cur.Brief, "agent.model": cur.Model,
		"agent.updated_at": strconv.FormatInt(s.now().Unix(), 10), "agent.updated_by": by}
	for k, v := range vals {
		if _, err := tx.ExecContext(ctx, `INSERT INTO meta(key, value) VALUES(?, ?)
			ON CONFLICT(key) DO UPDATE SET value = excluded.value`, k, v); err != nil {
			return AgentSettings{}, s.internal("settings", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return AgentSettings{}, s.internal("settings", err)
	}
	out, err := s.Settings(ctx)
	if err != nil {
		return AgentSettings{}, err
	}
	if err := s.writeProfile(out); err != nil {
		s.log.Error("cannot write the agent profile file", "err", err.Error())
		return out, newErr(http.StatusInternalServerError, CodeInternal, "The settings are saved, but the profile file could not be written; the agent keeps its previous settings until it is.")
	}
	s.log.Info("agent settings changed", "by", by)
	return out, nil
}

// WriteProfile writes the profile file from the stored settings; a server
// calls it at start so that the file exists and matches the database.
func (s *Service) WriteProfile(ctx context.Context) error {
	if s.cfg.AgentProfileFile == "" {
		return nil
	}
	cur, err := s.Settings(ctx)
	if err != nil {
		return err
	}
	return s.writeProfile(cur)
}

func (s *Service) writeProfile(a AgentSettings) error {
	path := s.cfg.AgentProfileFile
	if path == "" {
		return nil
	}
	data, err := json.MarshalIndent(map[string]any{
		"organisation": s.cfg.Org.Name, "brief": a.Brief, "model": a.Model, "updated_at": a.UpdatedAt,
	}, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".agent-profile-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if _, err := f.Write(append(data, '\n')); err != nil {
		f.Close()
		return err
	}
	if err := f.Chmod(0o640); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// --- admin.settings.* and admin.member.list ---

type settingsSetArgs struct {
	Brief          *string `json:"brief"`
	Model          *string `json:"model"`
	OpenJoinPaused *bool   `json:"open_join_paused"`
}

func (s *Service) adminSettingsGet(ctx context.Context, c *call) (any, error) {
	if err := decode(c.raw, &struct{}{}); err != nil {
		return nil, err
	}
	return s.Settings(ctx)
}

func (s *Service) adminSettingsSet(ctx context.Context, c *call) (any, error) {
	var a settingsSetArgs
	if err := decode(c.raw, &a); err != nil {
		return nil, err
	}
	if a.Brief == nil && a.Model == nil && a.OpenJoinPaused == nil {
		return nil, errMissing("brief, model or open_join_paused")
	}
	if a.OpenJoinPaused != nil {
		if err := s.SetOpenJoinPaused(ctx, adminName(c), *a.OpenJoinPaused); err != nil {
			return nil, err
		}
		if a.Brief == nil && a.Model == nil {
			return s.Settings(ctx)
		}
	}
	return s.SetSettings(ctx, adminName(c), a.Brief, a.Model)
}

type memberListArgs struct {
	Query string `json:"query"`
}

func (s *Service) adminMemberList(ctx context.Context, c *call) (any, error) {
	var a memberListArgs
	if err := decode(c.raw, &a); err != nil {
		return nil, err
	}
	q, err := text("query", a.Query, 64, false)
	if err != nil {
		return nil, err
	}
	list, err := s.ListMembers(ctx, q)
	if err != nil {
		return nil, err
	}
	return map[string]any{"members": list}, nil
}
