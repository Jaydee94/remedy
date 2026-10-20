package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/Jaydee94/remedy/internal/skill"
)

// Origins and states of a skill. A written skill is active when it is saved; a proposal is invisible to every run until it is
// confirmed; an off skill is kept and hidden.
const (
	SkillOriginWritten  = "written"
	SkillOriginProposed = "proposed"

	SkillProposed = "proposed"
	SkillActive   = "active"
	SkillOff      = "off"
)

// Activity kinds of the skills. The text of an entry names the skill and the host, never what the skill says.
const (
	KindSkillCreated   = "skill_created"
	KindSkillUpdated   = "skill_updated"
	KindSkillDeleted   = "skill_deleted"
	KindSkillConfirmed = "skill_confirmed"
	KindSkillRejected  = "skill_rejected"
	KindSkillsProposed = "skills_proposed"
)

var (
	// ErrScopeFull means the scope (one host, or global) already has skill.MaxPerScope skills.
	ErrScopeFull = errors.New("the scope has the most skills it may have")
	// ErrSkillState means the skill is in a state the operation does not work on: a proposal is only confirmed or rejected, and only a
	// proposal can be.
	ErrSkillState = errors.New("the skill is not in a state this works on")
)

// Skill is a short note about the lab, global (HostID 0) or for one host.
type Skill struct {
	ID          int64
	HostID      int64  // 0: a global skill
	HostName    string // empty for a global skill
	Name        string
	Description string
	Body        string
	Origin      string
	State       string
	RunID       string // the learning run that proposed it; empty for a written skill
	Evidence    []skill.Evidence
	ReplacesID  int64 // a proposal that replaces a skill points at it; 0 otherwise
	CreatedAt   time.Time
	UpdatedAt   time.Time
	ConfirmedAt *time.Time
}

const skillSelect = `
	SELECT s.id, s.host_id, COALESCE(h.name, ''), s.name, s.description, s.body, s.origin, s.state, s.run_id, s.evidence,
	       s.replaces_id, s.created_at, s.updated_at, s.confirmed_at
	FROM skills s LEFT JOIN hosts h ON h.id = s.host_id`

// skillOrder puts the global skills first, then the skills of each host by host name, each by name.
const skillOrder = ` ORDER BY (s.host_id IS NOT NULL), h.name, s.name, s.id`

func scanSkill(sc scanner) (Skill, error) {
	var (
		k                          Skill
		host, replaces             sql.NullInt64
		runID, confirmed           sql.NullString
		evidence, created, updated string
	)
	if err := sc.Scan(&k.ID, &host, &k.HostName, &k.Name, &k.Description, &k.Body, &k.Origin, &k.State, &runID, &evidence,
		&replaces, &created, &updated, &confirmed); err != nil {
		return Skill{}, err
	}
	k.HostID, k.ReplacesID, k.RunID = host.Int64, replaces.Int64, runID.String
	k.Evidence = skill.DecodeEvidence(evidence)
	var err error
	if k.CreatedAt, err = parseTS(created); err != nil {
		return Skill{}, err
	}
	if k.UpdatedAt, err = parseTS(updated); err != nil {
		return Skill{}, err
	}
	if k.ConfirmedAt, err = nullTS(confirmed); err != nil {
		return Skill{}, err
	}
	return k, nil
}

func (s *Store) querySkills(ctx context.Context, query string, args ...any) ([]Skill, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Skill{}
	for rows.Next() {
		k, err := scanSkill(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

// scopeText says where a skill lives, for an activity entry.
func scopeText(host string) string {
	if host == "" {
		return "global"
	}
	return "host " + host
}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return fmt.Sprintf("%d %ss", n, word)
}

// scopeCount is how many skills of a scope count against the limit: every one that is not a proposal.
func scopeCount(ctx context.Context, tx *sql.Tx, hostID int64) (int, error) {
	var n int
	err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM skills WHERE COALESCE(host_id, 0) = ? AND state <> 'proposed'`, hostID).Scan(&n)
	return n, err
}

// GetSkill returns one skill in any state; ErrNotFound if it does not exist.
func (s *Store) GetSkill(ctx context.Context, id int64) (Skill, error) {
	k, err := scanSkill(s.db.QueryRowContext(ctx, skillSelect+` WHERE s.id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Skill{}, ErrNotFound
	}
	return k, err
}

// SkillFilter selects skills. HostID 0 means every scope (HostID selects that host's skills only, not the global ones); State "" means
// every state.
type SkillFilter struct {
	HostID int64
	State  string
}

// ListSkills returns the skills that match, global first, then by host name, then by name. It never returns nil.
func (s *Store) ListSkills(ctx context.Context, f SkillFilter) ([]Skill, error) {
	q := skillSelect + ` WHERE 1 = 1`
	var args []any
	if f.HostID != 0 {
		q += ` AND s.host_id = ?`
		args = append(args, f.HostID)
	}
	if f.State != "" {
		q += ` AND s.state = ?`
		args = append(args, f.State)
	}
	return s.querySkills(ctx, q+skillOrder, args...)
}

// CreateSkill saves a written skill, active, for a host (hostID) or global (0), and logs it. It returns a *skill.FieldError for a text
// that breaks the rules, ErrNotFound for an unknown host, ErrExists if the scope has a skill of that name and ErrScopeFull at the limit.
func (s *Store) CreateSkill(ctx context.Context, hostID int64, f skill.Fields) (Skill, error) {
	f, err := f.Validate()
	if err != nil {
		return Skill{}, err
	}
	var id int64
	err = s.inTx(ctx, func(tx *sql.Tx) error {
		host := ""
		if hostID != 0 {
			if host, err = hostName(ctx, tx, hostID); err != nil {
				return err
			}
		}
		n, err := scopeCount(ctx, tx, hostID)
		if err != nil {
			return err
		}
		if n >= skill.MaxPerScope {
			return ErrScopeFull
		}
		now := formatTS(time.Now())
		res, err := tx.ExecContext(ctx, `
			INSERT INTO skills (host_id, name, description, body, origin, state, created_at, updated_at)
			VALUES (?, ?, ?, ?, 'written', 'active', ?, ?)`, nullInt(hostID), f.Name, f.Description, f.Body, now, now)
		if err != nil {
			if isUnique(err) {
				return ErrExists
			}
			return err
		}
		if id, err = res.LastInsertId(); err != nil {
			return err
		}
		return insertActivity(ctx, tx, NewActivity{Kind: KindSkillCreated, Summary: fmt.Sprintf("Skill %s created (%s)", f.Name, scopeText(host))})
	})
	if err != nil {
		return Skill{}, err
	}
	return s.GetSkill(ctx, id)
}

// UpdateSkill replaces the texts and the state (active or off) of a skill that is not a proposal. The scope and the origin never
// change. It returns ErrNotFound, ErrSkillState for a proposal, ErrExists if the new name is taken in the scope, a *skill.FieldError.
func (s *Store) UpdateSkill(ctx context.Context, id int64, f skill.Fields, state string) (Skill, error) {
	if state != SkillActive && state != SkillOff {
		return Skill{}, &skill.FieldError{Field: "state", Msg: "The state must be active or off."}
	}
	f, err := f.Validate()
	if err != nil {
		return Skill{}, err
	}
	err = s.inTx(ctx, func(tx *sql.Tx) error {
		cur, err := scanSkill(tx.QueryRowContext(ctx, skillSelect+` WHERE s.id = ?`, id))
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if cur.State == SkillProposed {
			return ErrSkillState
		}
		if _, err := tx.ExecContext(ctx, `UPDATE skills SET name = ?, description = ?, body = ?, state = ?, updated_at = ? WHERE id = ?`,
			f.Name, f.Description, f.Body, state, formatTS(time.Now()), id); err != nil {
			if isUnique(err) {
				return ErrExists
			}
			return err
		}
		summary := fmt.Sprintf("Skill %s changed (%s)", f.Name, scopeText(cur.HostName))
		if f.Name == cur.Name && f.Description == cur.Description && f.Body == cur.Body && state != cur.State {
			if state == SkillOff {
				summary = fmt.Sprintf("Skill %s turned off (%s)", f.Name, scopeText(cur.HostName))
			} else {
				summary = fmt.Sprintf("Skill %s turned on (%s)", f.Name, scopeText(cur.HostName))
			}
		}
		return insertActivity(ctx, tx, NewActivity{Kind: KindSkillUpdated, Summary: summary})
	})
	if err != nil {
		return Skill{}, err
	}
	return s.GetSkill(ctx, id)
}

// DeleteSkill removes a skill that is not a proposal (a proposal is rejected) and logs it.
func (s *Store) DeleteSkill(ctx context.Context, id int64) error {
	return s.inTx(ctx, func(tx *sql.Tx) error {
		cur, err := scanSkill(tx.QueryRowContext(ctx, skillSelect+` WHERE s.id = ?`, id))
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if cur.State == SkillProposed {
			return ErrSkillState
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM skills WHERE id = ?`, id); err != nil {
			return err
		}
		return insertActivity(ctx, tx, NewActivity{Kind: KindSkillDeleted, Summary: fmt.Sprintf("Skill %s deleted (%s)", cur.Name, scopeText(cur.HostName))})
	})
}
