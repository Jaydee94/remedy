package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/Jaydee94/remedy/internal/skill"
)

// Proposal is one skill that a learning run proposes. The evidence is stored as it is given; the caller (the learner) has checked each
// cited command against what the run really did.
type Proposal struct {
	skill.Fields
	Evidence []skill.Evidence
}

// ProposalSet is what a learning run proposes for one host.
type ProposalSet struct {
	HostID int64
	RunID  string // the learning run; empty only in a test
	Items  []Proposal
}

// CreateProposals stores a learning run's proposals for one host, all or none. Every text is redacted (the agent saw host output) and
// then validated: a *skill.FieldError names the first problem and nothing is stored. A pending proposal of the same host and name is
// replaced by the new one; a proposal whose name is that of an active or off skill of the host points at it (replaces_id). Proposals do
// not count against the limit of a scope, and they are invisible to every run until they are confirmed. ErrNotFound for an unknown host.
func (s *Store) CreateProposals(ctx context.Context, p ProposalSet) ([]Skill, error) {
	if len(p.Items) == 0 {
		return []Skill{}, nil
	}
	if len(p.Items) > skill.MaxProposalsPerRun {
		return nil, &skill.FieldError{Field: "skills", Msg: fmt.Sprintf("At most %d skills may be proposed at a time.", skill.MaxProposalsPerRun)}
	}
	items := make([]Proposal, 0, len(p.Items))
	seen := map[string]bool{}
	for _, it := range p.Items {
		f, err := skill.Redacted(it.Fields).Validate()
		if err != nil {
			return nil, err
		}
		if seen[f.Name] {
			return nil, &skill.FieldError{Field: "name", Msg: fmt.Sprintf("The name %s is proposed twice.", f.Name)}
		}
		seen[f.Name] = true
		items = append(items, Proposal{Fields: f, Evidence: it.Evidence})
	}

	var ids []int64
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		host, err := hostName(ctx, tx, p.HostID)
		if err != nil {
			return err
		}
		now := formatTS(time.Now())
		for _, it := range items {
			if _, err := tx.ExecContext(ctx, `DELETE FROM skills WHERE host_id = ? AND name = ? AND state = 'proposed'`, p.HostID, it.Name); err != nil {
				return err
			}
			var replaces sql.NullInt64
			err := tx.QueryRowContext(ctx, `SELECT id FROM skills WHERE host_id = ? AND name = ? AND state <> 'proposed'`, p.HostID, it.Name).Scan(&replaces)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return err
			}
			res, err := tx.ExecContext(ctx, `
				INSERT INTO skills (host_id, name, description, body, origin, state, run_id, evidence, replaces_id, created_at, updated_at)
				VALUES (?, ?, ?, ?, 'proposed', 'proposed', ?, ?, ?, ?, ?)`,
				p.HostID, it.Name, it.Description, it.Body, nullString(p.RunID), skill.EncodeEvidence(it.Evidence), replaces, now, now)
			if err != nil {
				return err
			}
			id, err := res.LastInsertId()
			if err != nil {
				return err
			}
			ids = append(ids, id)
		}
		return insertActivity(ctx, tx, NewActivity{
			Kind: KindSkillsProposed, RunID: p.RunID,
			Summary: fmt.Sprintf("Learned about %s: %s", host, plural(len(items), "proposal")),
		})
	})
	if err != nil {
		return nil, err
	}
	out := make([]Skill, 0, len(ids))
	for _, id := range ids {
		k, err := s.GetSkill(ctx, id)
		if err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, nil
}

// ConfirmSkill turns a proposal into a skill the runs see. edit, if not nil, replaces the description and the body (the name is the
// proposal's); it is validated like any skill. A proposal that replaces a skill that still exists updates that row in place (its id
// stays), makes it active and deletes the proposal; otherwise the proposal itself becomes active. The origin stays "proposed" and
// confirmed_at is set. A new skill in a scope that has the most it may have is refused (ErrScopeFull), and so is a name that another
// skill took meanwhile (ErrExists): the proposal then stays a proposal.
func (s *Store) ConfirmSkill(ctx context.Context, id int64, edit *skill.Fields) (Skill, error) {
	var result int64
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		cur, err := scanSkill(tx.QueryRowContext(ctx, skillSelect+` WHERE s.id = ?`, id))
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if cur.State != SkillProposed {
			return ErrSkillState
		}
		desc, body := cur.Description, cur.Body
		if edit != nil {
			f, err := skill.Fields{Name: cur.Name, Description: edit.Description, Body: edit.Body}.Validate()
			if err != nil {
				return err
			}
			desc, body = f.Description, f.Body
		}
		now := formatTS(time.Now())
		var target int64
		if cur.ReplacesID != 0 {
			err := tx.QueryRowContext(ctx, `SELECT id FROM skills WHERE id = ? AND COALESCE(host_id, 0) = ? AND state <> 'proposed'`, cur.ReplacesID, cur.HostID).Scan(&target)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return err
			}
		}
		summary := fmt.Sprintf("Skill %s confirmed (%s)", cur.Name, scopeText(cur.HostName))
		if target != 0 {
			if _, err := tx.ExecContext(ctx, `
				UPDATE skills SET description = ?, body = ?, origin = 'proposed', state = 'active', run_id = ?, evidence = ?, replaces_id = NULL,
					updated_at = ?, confirmed_at = ?
				WHERE id = ?`, desc, body, nullString(cur.RunID), skill.EncodeEvidence(cur.Evidence), now, now, target); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `DELETE FROM skills WHERE id = ?`, cur.ID); err != nil {
				return err
			}
			result = target
			summary += "; it replaces the earlier text"
		} else {
			n, err := scopeCount(ctx, tx, cur.HostID)
			if err != nil {
				return err
			}
			if n >= skill.MaxPerScope {
				return ErrScopeFull
			}
			if _, err := tx.ExecContext(ctx, `
				UPDATE skills SET description = ?, body = ?, state = 'active', replaces_id = NULL, updated_at = ?, confirmed_at = ?
				WHERE id = ?`, desc, body, now, now, cur.ID); err != nil {
				if isUnique(err) {
					return ErrExists
				}
				return err
			}
			result = cur.ID
		}
		return insertActivity(ctx, tx, NewActivity{Kind: KindSkillConfirmed, RunID: cur.RunID, Summary: summary})
	})
	if err != nil {
		return Skill{}, err
	}
	return s.GetSkill(ctx, result)
}

// RejectSkill deletes a proposal and logs that it was rejected; the activity log keeps that it existed.
func (s *Store) RejectSkill(ctx context.Context, id int64) error {
	return s.inTx(ctx, func(tx *sql.Tx) error {
		cur, err := scanSkill(tx.QueryRowContext(ctx, skillSelect+` WHERE s.id = ?`, id))
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if cur.State != SkillProposed {
			return ErrSkillState
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM skills WHERE id = ?`, id); err != nil {
			return err
		}
		return insertActivity(ctx, tx, NewActivity{Kind: KindSkillRejected, RunID: cur.RunID, Summary: fmt.Sprintf("Proposal %s rejected (%s)", cur.Name, scopeText(cur.HostName))})
	})
}
