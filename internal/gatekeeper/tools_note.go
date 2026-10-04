package gatekeeper

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/Jaydee94/remedy/internal/store"
)

const (
	maxNoteRunes = 2000
	noteSchema   = `{"type":"object","properties":{"id":{"type":"integer","minimum":1,"description":"The incident id."},"note":{"type":"string","minLength":1,"maxLength":2000,"description":"The note."}},"required":["id","note"],"additionalProperties":false}`
)

type noteArgs struct {
	ID   int64  `json:"id"`
	Note string `json:"note"`
}

// NoteTool is the mutating tool of this part: it adds a note to an incident. It changes only Remedy's own database,
// and still needs the maintainer's approval, which is what makes it a test of the whole mechanism.
func NoteTool(st *store.Store) Tool {
	return Tool{
		Name:        "incident_add_note",
		Description: "Adds a note to an incident. The note shows in the incident's history for the maintainer. This changes data, so the maintainer has to approve the call first; the call waits until they decide.",
		Mutating:    true,
		Schema:      json.RawMessage(noteSchema),
		Decode: func(raw json.RawMessage) (json.RawMessage, error) {
			var a noteArgs
			if err := DecodeArgs(raw, &a); err != nil {
				return nil, err
			}
			if a.ID <= 0 {
				return nil, ArgumentError("id must be a positive whole number")
			}
			if strings.TrimSpace(a.Note) == "" {
				return nil, ArgumentError("note must not be empty")
			}
			if utf8.RuneCountInString(a.Note) > maxNoteRunes {
				return nil, ArgumentError(fmt.Sprintf("note must have at most %d characters", maxNoteRunes))
			}
			return json.Marshal(a)
		},
		Check: func(ctx context.Context, args json.RawMessage) error {
			id := idOf(args)
			if _, err := st.GetIncident(ctx, id); errors.Is(err, store.ErrNotFound) {
				return ArgumentError(fmt.Sprintf("there is no incident %d", id))
			} else if err != nil {
				return err
			}
			return nil
		},
		Incident: idOf,
		Run: func(ctx context.Context, c Call) (string, error) {
			var a noteArgs
			if err := json.Unmarshal(c.Args, &a); err != nil {
				return "", err
			}
			if err := st.AddNote(ctx, a.ID, c.RunID, a.Note); errors.Is(err, store.ErrNotFound) {
				return "", ArgumentError(fmt.Sprintf("there is no incident %d", a.ID))
			} else if err != nil {
				return "", err
			}
			return "note added", nil
		},
	}
}
