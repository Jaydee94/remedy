package incident

import (
	"testing"

	"github.com/Jaydee94/remedy/internal/store"
)

func TestReopenedTextWordsTheOrdinal(t *testing.T) {
	o := Observation{Source: store.SourceAlertmanager, Title: "HighLoad nas"}
	for n, want := range map[int]string{
		1: "1st", 2: "2nd", 3: "3rd", 4: "4th", 10: "10th", 11: "11th", 12: "12th", 13: "13th", 14: "14th",
		21: "21st", 22: "22nd", 23: "23rd", 101: "101st", 111: "111th", 112: "112th", 113: "113th",
	} {
		if got := reopenedText(o, n); got != "Incident reopened: alert HighLoad nas fired again ("+want+" time)" {
			t.Errorf("reopenedText(%d) = %q, want the %s", n, got, want)
		}
	}
}
