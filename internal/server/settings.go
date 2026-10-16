package server

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/Jaydee94/remedy/internal/settings"
)

// maxSettingBody leaves room for the longest secret (4096 bytes, twice that when every byte is escaped in JSON).
const maxSettingBody = 16 << 10

// Fact is something about the process that is set outside the Setup page, shown there as a read-only fact. It never holds a secret.
type Fact struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

type settingView struct {
	Key      string          `json:"key"`
	Group    string          `json:"group"`
	Label    string          `json:"label"`
	Kind     string          `json:"kind"`
	Value    json.RawMessage `json:"value"`
	Source   string          `json:"source"`
	Fallback string          `json:"fallback"` // where the value comes from after a reset
	Env      string          `json:"env"`
	Rule     string          `json:"rule"`
	Effect   string          `json:"effect"`
	Problem  string          `json:"problem,omitempty"`
	Hint     string          `json:"hint,omitempty"` // a secret's last characters; its value is always null
}

func settingViewOf(e settings.Entry) settingView {
	return settingView{
		Key: e.Spec.Key, Group: e.Spec.Group, Label: e.Spec.Label, Kind: string(e.Spec.Kind), Value: e.Value.JSON(e.Spec.Kind),
		Source: string(e.Source), Fallback: string(e.Fallback), Env: e.Spec.Env, Rule: e.Spec.Rule, Effect: e.Spec.Effect, Problem: e.Problem,
		Hint: e.Hint,
	}
}

func (s *srv) getSettings(w http.ResponseWriter, _ *http.Request) {
	entries := s.d.Settings.Entries()
	views := make([]settingView, 0, len(entries))
	for _, e := range entries {
		views = append(views, settingViewOf(e))
	}
	facts := s.d.Facts
	if facts == nil {
		facts = []Fact{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"settings": views, "facts": facts})
}

// putSetting hands the key from the path to Settings.Set, which refuses an unknown one; the key never reaches a getter that panics.
func (s *srv) putSetting(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Value json.RawMessage `json:"value"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxSettingBody)).Decode(&body); err != nil || len(body.Value) == 0 {
		writeErr(w, http.StatusBadRequest, `send {"value": ...}`)
		return
	}
	e, err := s.d.Settings.Set(r.Context(), r.PathValue("key"), body.Value)
	var invalid *settings.InvalidError
	switch {
	case errors.Is(err, settings.ErrUnknownKey):
		writeErr(w, http.StatusNotFound, "setting not found")
	case errors.As(err, &invalid):
		writeErr(w, http.StatusBadRequest, invalid.Message)
	case err != nil:
		writeErr(w, http.StatusInternalServerError, "could not save the setting")
	default:
		writeJSON(w, http.StatusOK, settingViewOf(e))
	}
}

func (s *srv) deleteSetting(w http.ResponseWriter, r *http.Request) {
	_, err := s.d.Settings.Delete(r.Context(), r.PathValue("key"))
	switch {
	case errors.Is(err, settings.ErrUnknownKey):
		writeErr(w, http.StatusNotFound, "setting not found")
	case err != nil:
		writeErr(w, http.StatusInternalServerError, "could not reset the setting")
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}
