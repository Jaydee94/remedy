// Package learner starts the learning run of a host and stores what it finds. The agent that learns can only read the host (host_list,
// host_read, skill_get); it has no way to store anything. This package is the only place that does, and only after the control plane
// validated the answer, as proposals that no run sees until the maintainer confirms them.
package learner

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/Jaydee94/remedy/internal/diagnosis"
	"github.com/Jaydee94/remedy/internal/prompt"
	"github.com/Jaydee94/remedy/internal/run"
	"github.com/Jaydee94/remedy/internal/skill"
	"github.com/Jaydee94/remedy/internal/store"
)

// NotReadyError says that a host cannot be learned about now, and why. The reason is a fixed sentence.
type NotReadyError struct{ Host, Reason string }

func (e *NotReadyError) Error() string { return "the host " + e.Host + " is not ready: " + e.Reason }

// NotReady says why a learning run cannot use the host, or "" when it can: the host is enabled, has a login and a pinned key, and its
// last connection test worked. (A new pin or a new login makes the host untested again; test the connection first.)
func NotReady(e store.HostEntry) string {
	switch {
	case !e.Host.Enabled:
		return "it is disabled"
	case e.Credential == nil:
		return "it has no login"
	case e.Host.PinnedKey == "":
		return "its key is not pinned"
	case e.Host.Status != store.HostOK:
		return "its last connection test did not succeed; test the connection first"
	}
	return ""
}

// Learner starts learning runs and closes them. Use it by pointer: it must not be copied.
type Learner struct {
	Store    *store.Store
	Provider string // default "claude"
	Log      *slog.Logger
	Now      func() time.Time // default time.Now

	// complete serialises Complete, so that its check for a recorded outcome and the write of the outcome cannot interleave with a
	// second call for the same run (the finish handler and the reaper).
	complete sync.Mutex
}

func (l *Learner) provider() string {
	if l.Provider != "" {
		return l.Provider
	}
	return "claude"
}

func (l *Learner) now() time.Time {
	if l.Now != nil {
		return l.Now()
	}
	return time.Now()
}

func (l *Learner) logger() *slog.Logger {
	if l.Log != nil {
		return l.Log
	}
	return slog.Default()
}

// Start queues the learning run of a host. It returns store.ErrNotFound for an unknown host, a *NotReadyError, store.ErrLearning,
// store.ErrBusy or store.ErrProposalsWaiting; it creates nothing then.
//
// The readiness of the host is checked here, outside StartLearning's transaction, so the host can change between the check and the
// start (disabled, unpinned, its login removed). That is acceptable: the check only spares the maintainer a run that cannot work, and it
// is not the barrier. The host tools check that the host is enabled, has a login and a pinned key again at every call.
func (l *Learner) Start(ctx context.Context, hostID int64) (run.Run, error) {
	e, err := l.Store.GetHost(ctx, hostID)
	if err != nil {
		return run.Run{}, err
	}
	if why := NotReady(e); why != "" {
		return run.Run{}, &NotReadyError{Host: e.Host.Name, Reason: why}
	}
	// The skills the host has, so that the agent proposes an update under the same name; a proposal is invisible and is not listed.
	have, err := l.Store.ListSkills(ctx, store.SkillFilter{HostID: hostID})
	if err != nil {
		return run.Run{}, err
	}
	var existing []prompt.LearnSkill
	for _, k := range have {
		if k.State != store.SkillProposed {
			existing = append(existing, prompt.LearnSkill{Name: k.Name, Description: k.Description, Off: k.State == store.SkillOff})
		}
	}
	text := prompt.Learn(prompt.LearnInput{
		HostName: e.Host.Name, Notes: e.Host.Notes, DockerPattern: e.Host.DockerPattern, Units: e.Host.SystemdUnits, Skills: existing,
	}, "")
	return l.Store.StartLearning(ctx, store.LearnStart{HostID: hostID, Provider: l.provider(), Prompt: text, Now: l.now()})
}

// CheckRun judges what the runner reports for a learning run: a run that exited with 0 must carry a valid answer. Otherwise the outcome
// becomes a failure with the reason invalid_output. Nothing is stored for it.
func CheckRun(o run.Outcome) run.Outcome {
	if o.ExitCode != 0 || o.FailureReason != "" {
		return o
	}
	if len(o.Output) == 0 || string(o.Output) == "null" {
		o.FailureReason = run.ReasonInvalidOutput
		o.Result = "The agent finished without a structured answer."
		return o
	}
	if _, err := diagnosis.ParseLearn(o.Output); err != nil {
		o.FailureReason = run.ReasonInvalidOutput
		o.Result = "The agent's answer is not a valid learning answer: " + err.Error()
	}
	return o
}

// Complete closes a learning run that has ended (the server calls it after the runner reported, the reaper after it failed a run). A
// succeeded run's answer is parsed again, its cited commands are checked against the host_read calls that succeeded, and the profile
// and the skills are stored as proposals in one transaction. Any other end, an answer that does not parse and a store that refuses leave
// nothing behind and only log an activity entry. Calling it for another role, a run that has not ended or a host that is gone does
// nothing.
//
// Complete may be called more than once for a run (the finish handler and the reaper's OnFailed): a run whose outcome is already in the
// activity log (skills_proposed or learn_failed) is left alone, so a second call stores no second set, does not bring back a proposal
// that was rejected meanwhile and logs no second failure. The check and the write are not one store transaction (the failure is a
// separate AddActivity call); the mutex of the Learner serialises them within the control plane, which is the only process that calls
// Complete.
func (l *Learner) Complete(ctx context.Context, runID string) {
	l.complete.Lock()
	defer l.complete.Unlock()
	rn, err := l.Store.GetRun(ctx, runID)
	if err != nil {
		if !errors.Is(err, store.ErrNotFound) {
			l.logger().Error("cannot read a learning run", "run", runID, "err", err)
		}
		return
	}
	if rn.Role != run.RoleLearner || !rn.Status.Terminal() {
		return
	}
	switch done, err := l.Store.LearnOutcomeRecorded(ctx, runID); {
	case err != nil:
		l.logger().Error("cannot read the outcome of a learning run", "run", runID, "err", err)
		return
	case done:
		return
	}
	hosts, err := l.Store.RunHosts(ctx, runID)
	if err != nil {
		l.logger().Error("cannot read the host of a learning run", "run", runID, "err", err)
		return
	}
	if len(hosts) == 0 {
		return // the host was deleted: its proposals went with it, and there is nobody to tell
	}
	host := hosts[0].Host

	if rn.Status != run.Succeeded {
		l.fail(ctx, rn, host.Name, failureText(rn))
		return
	}
	answer, err := diagnosis.ParseLearn(rn.Output)
	if err != nil {
		l.fail(ctx, rn, host.Name, "the answer was not valid")
		return
	}
	reads, err := l.Store.SucceededReads(ctx, runID)
	if err != nil {
		l.logger().Error("cannot read the calls of a learning run", "run", runID, "err", err)
		l.fail(ctx, rn, host.Name, "the proposals could not be stored")
		return
	}
	_, err = l.Store.CreateProposals(ctx, store.ProposalSet{HostID: host.ID, RunID: runID, Items: Proposals(answer, reads)})
	switch {
	case err == nil:
	case errors.Is(err, store.ErrNotFound):
		// the host went between the two reads
	case errors.Is(err, store.ErrTooManyProposals):
		l.fail(ctx, rn, host.Name, "too many proposals wait for a review; confirm or reject some first")
	default:
		l.logger().Error("cannot store the proposals of a learning run", "run", runID, "err", err)
		l.fail(ctx, rn, host.Name, "the proposals could not be stored")
	}
}

func (l *Learner) fail(ctx context.Context, rn run.Run, host, why string) {
	err := l.Store.AddActivity(ctx, store.NewActivity{
		Kind: store.KindLearnFailed, RunID: rn.ID, Summary: fmt.Sprintf("Learning about %s failed: %s", host, why),
	})
	if err != nil {
		l.logger().Error("cannot log a failed learning run", "run", rn.ID, "err", err)
	}
}

func failureText(rn run.Run) string {
	switch {
	case rn.FailureReason == run.ReasonTimeout:
		return "the run timed out"
	case rn.FailureReason == run.ReasonCancelled:
		return "the run was cancelled"
	case rn.FailureReason == run.ReasonInvalidOutput:
		return "the answer was not valid"
	case rn.FailureReason == run.ReasonRunnerLost:
		return "the runner was lost"
	case rn.ExitCode != nil && *rn.ExitCode != 0:
		return fmt.Sprintf("the agent exited with code %d", *rn.ExitCode)
	}
	return "the run failed"
}

// maxProfileEvidence is how many of the commands the learner ran the profile cites.
const maxProfileEvidence = 10

// Proposals turns a validated answer into the proposals to store: the profile first, then the skills in the order of the answer. reads
// are the commands of the host_read calls that succeeded. A cited command is "ran" only when the words are exactly those of one of
// them; the profile cites the first commands the learner really ran.
//
// "Ran" means that the command was run on the host, not that it succeeded there: a host_read call succeeds when the gatekeeper ran the
// command and returned its output, which includes a command that exited with a non-zero code. A refused call (a command outside the
// table of reads) and a call that failed (an unreachable host, a timeout) did not run.
func Proposals(a diagnosis.Learn, reads [][]string) []store.Proposal {
	ran := func(argv []string) bool {
		for _, r := range reads {
			if slices.Equal(r, argv) {
				return true
			}
		}
		return false
	}
	var profileEvidence []skill.Evidence
	for _, r := range reads {
		if len(profileEvidence) == maxProfileEvidence {
			break
		}
		if slices.ContainsFunc(profileEvidence, func(e skill.Evidence) bool { return slices.Equal(e.Argv, r) }) {
			continue // the same command twice is cited once
		}
		profileEvidence = append(profileEvidence, skill.Evidence{Argv: r, Ran: true})
	}
	out := []store.Proposal{{
		Fields:   skill.Fields{Name: skill.ProfileName, Description: ProfileDescription(a.Profile), Body: ProfileBody(a.Profile)},
		Evidence: profileEvidence,
	}}
	for _, k := range a.Skills {
		var ev []skill.Evidence
		for _, argv := range k.Evidence {
			ev = append(ev, skill.Evidence{Argv: argv, Ran: ran(argv)})
		}
		out = append(out, store.Proposal{Fields: skill.Fields{Name: k.Name, Description: k.Description, Body: k.Body}, Evidence: ev})
	}
	return out
}

// flat makes text one line: whitespace of every kind becomes one space.
func flat(s string) string { return strings.Join(strings.Fields(s), " ") }

// ProfileDescription is the one line that the list of skills shows for the profile, at most 200 characters; a longer line ends at a word
// and with "…" (skill.CutDescription).
func ProfileDescription(p diagnosis.LearnProfile) string {
	return skill.CutDescription("What this host is: " + flat(p.OS) + "; " + flat(p.Role))
}

// escapeBlock puts a backslash before a one-line value that starts with a Markdown block marker (a heading, a quote, a list item, a
// thematic break, a setext underline, a fence, a table row), so that the value is shown as text and never starts a block of its own.
func escapeBlock(s string) string {
	if s == "" {
		return s
	}
	switch s[0] {
	case '#', '>', '-', '+', '*', '=', '_', '`', '|', '~':
		return `\` + s
	}
	digits := len(s) - len(strings.TrimLeft(s, "0123456789"))
	if digits > 0 && digits < len(s) && (s[digits] == '.' || s[digits] == ')') {
		return `\` + s
	}
	return s
}

// field makes an agent's string one safe line of the note: whitespace of every kind becomes one space and a leading block marker is
// escaped.
func field(s string) string { return escapeBlock(flat(s)) }

// lineBreaks are the characters that end a line of the notes. A renderer may break a line at any of them, so each one starts a new
// line of the block quote.
var lineBreaks = strings.NewReplacer("\r\n", "\n", "\r", "\n", "\v", "\n", "\f", "\n", string(rune(0x85)), "\n", string(rune(0x2028)), "\n", string(rune(0x2029)), "\n")

// ProfileBody writes the profile as one Markdown note. Remedy writes the structure, the headings and the list markers; the agent's
// strings only fill the fields. A one-line field is made one line, so that a line break in it cannot start a heading of its own, and the
// notes are a block quote for the same reason; every value and every line of the notes that starts with a block marker is escaped. The
// note is shown as text and is data like every skill.
//
// The note always fits a skill (skill.MaxBodyBytes): an answer within the limits of diagnosis.ParseLearn can still be longer (many items
// of multi-byte characters, notes of many short lines), and Remedy, which writes the note, cuts it rather than refuse the answer. The
// cut is deterministic and says so: first the notes (at a line, else at a character, then the quoted line "(cut: too long)"), then the
// containers and then the services (items from the end, then the line "- ... (N more not shown)"). The headings and the lines of the
// operating system and the role always stay.
func ProfileBody(p diagnosis.LearnProfile) string {
	head := "## Operating system\n\n" + field(p.OS) + "\n\n" + "## Role\n\n" + field(p.Role) + "\n\n"
	services, containers := listLines(p.Services), listLines(p.Containers)
	var notes []string
	if strings.TrimSpace(p.Notes) != "" {
		for _, line := range strings.Split(lineBreaks.Replace(p.Notes), "\n") {
			if line = strings.TrimSpace(line); line == "" {
				notes = append(notes, ">\n")
			} else {
				notes = append(notes, "> "+escapeBlock(line)+"\n")
			}
		}
	}
	svc := listSection("Services", services, 0)
	ctr := listSection("Containers", containers, 0)
	nts := notesSection(notes, false)
	if len(head)+len(svc)+len(ctr)+len(nts) <= skill.MaxBodyBytes {
		return head + svc + ctr + nts
	}
	if len(notes) > 0 {
		room := skill.MaxBodyBytes - len(head) - len(svc) - len(ctr) - len(notesSection(nil, true))
		nts = notesSection(fitNotes(notes, room), true)
	}
	if len(head)+len(svc)+len(ctr)+len(nts) > skill.MaxBodyBytes {
		ctr = cutList("Containers", containers, skill.MaxBodyBytes-len(head)-len(svc)-len(nts))
	}
	if len(head)+len(svc)+len(ctr)+len(nts) > skill.MaxBodyBytes {
		svc = cutList("Services", services, skill.MaxBodyBytes-len(head)-len(ctr)-len(nts))
	}
	return head + svc + ctr + nts
}

// cutNote ends notes that were cut.
const cutNote = "> (cut: too long)\n"

// listLines makes each item one list line of the note.
func listLines(items []string) []string {
	out := make([]string, 0, len(items))
	for _, s := range items {
		out = append(out, "- "+field(s)+"\n")
	}
	return out
}

// listSection writes a list of the profile; more is the number of items that are not shown.
func listSection(title string, lines []string, more int) string {
	s := "## " + title + "\n\n"
	if len(lines) == 0 && more == 0 {
		return s + "None found.\n\n"
	}
	s += strings.Join(lines, "")
	if more > 0 {
		s += "- " + string(rune(0x2026)) + " (" + strconv.Itoa(more) + " more not shown)\n"
	}
	return s + "\n"
}

// cutList writes a list with as many of its first lines as fit room bytes, and the line that counts the others.
func cutList(title string, lines []string, room int) string {
	for k := len(lines) - 1; k > 0; k-- {
		if s := listSection(title, lines[:k], len(lines)-k); len(s) <= room {
			return s
		}
	}
	return listSection(title, nil, len(lines))
}

// notesSection writes the notes; cut ends them with cutNote.
func notesSection(lines []string, cut bool) string {
	s := "## Notes\n\n"
	if len(lines) == 0 && !cut {
		return s + "None.\n"
	}
	s += strings.Join(lines, "")
	if cut {
		s += cutNote
	}
	return s
}

// fitNotes keeps the first lines of the notes that fit room bytes; a line that does not fit whole is cut at a character. A prefix of an
// escaped line starts like the line, so it starts no block of its own.
func fitNotes(lines []string, room int) []string {
	var out []string
	for _, l := range lines {
		if len(l) <= room {
			out = append(out, l)
			room -= len(l)
			continue
		}
		if l == ">\n" || room < len("> x\n") {
			break
		}
		text := l[len("> ") : len(l)-1]
		end := room - len("> \n")
		for end > 0 && !utf8.RuneStart(text[end]) {
			end--
		}
		if text = strings.TrimRight(text[:end], " \t"); text != "" {
			out = append(out, "> "+text+"\n")
		}
		break
	}
	return out
}
