package store

import (
	"context"
	"strings"
)

// VisibleSkills returns the skills a run with these hosts may see: the active ones that are global, and the active ones of each of the
// hosts. A proposal, an off skill and the skill of any other host are never in it. Global skills first, then by host name, then by name.
func (s *Store) VisibleSkills(ctx context.Context, hostIDs []int64) ([]Skill, error) {
	q := skillSelect + ` WHERE s.state = 'active' AND (s.host_id IS NULL`
	var args []any
	if len(hostIDs) > 0 {
		q += ` OR s.host_id IN (` + strings.TrimSuffix(strings.Repeat("?,", len(hostIDs)), ",") + `)`
		for _, id := range hostIDs {
			args = append(args, id)
		}
	}
	return s.querySkills(ctx, q+`)`+skillOrder, args...)
}

// RunSkills is VisibleSkills for the hosts bound to a run (run_hosts: a host that was deleted is not bound any more). The set is read
// at the time of the call, so a skill turned off or deleted since the run started is gone. An unknown run sees the global skills.
func (s *Store) RunSkills(ctx context.Context, runID string) ([]Skill, error) {
	return s.querySkills(ctx, skillSelect+`
		WHERE s.state = 'active' AND (s.host_id IS NULL OR s.host_id IN (SELECT host_id FROM run_hosts WHERE run_id = ?))`+skillOrder, runID)
}
