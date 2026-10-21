package loginrelay

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
)

const validURL = "https://claude.com/cai/oauth/authorize?code=true&client_id=c-1&response_type=code&redirect_uri=r-1&scope=s-1&code_challenge=ch-1&code_challenge_method=S256&state=st-1"

type caseFile struct {
	URLs []struct {
		Name  string `json:"name"`
		Input string `json:"input"`
		Valid bool   `json:"valid"`
		PadTo int    `json:"padTo"`
	} `json:"urls"`
	Codes []struct {
		Name    string `json:"name"`
		Input   string `json:"input"`
		Valid   bool   `json:"valid"`
		Trimmed string `json:"trimmed"`
		PadTo   int    `json:"padTo"`
	} `json:"codes"`
}

// expand replaces the tokens of the shared cases and pads the input with 'a'
// until it has padTo bytes. The tokens keep invisible and non-ASCII characters
// out of the source files.
func expand(s string, padTo int) string {
	r := strings.NewReplacer(
		"<VALID>", validURL,
		"<NUL>", string(rune(0)),
		"<TAB>", string(rune(9)),
		"<LF>", string(rune(10)),
		"<CR>", string(rune(13)),
		"<BS>", string(rune(8)),
		"<SP>", string(rune(32)),
		"<CYR>", string(rune(0x430)),
	)
	out := r.Replace(s)
	if padTo > len(out) {
		out += strings.Repeat("a", padTo-len(out))
	}
	return out
}

func loadCases(t *testing.T) caseFile {
	t.Helper()
	b, err := os.ReadFile("testdata/cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var c caseFile
	if err := json.Unmarshal(b, &c); err != nil {
		t.Fatal(err)
	}
	if len(c.URLs) == 0 || len(c.Codes) == 0 {
		t.Fatal("no cases")
	}
	return c
}

func TestValidateURLCases(t *testing.T) {
	for _, c := range loadCases(t).URLs {
		t.Run(c.Name, func(t *testing.T) {
			in := expand(c.Input, c.PadTo)
			err := ValidateURL(in)
			if c.Valid && err != nil {
				t.Fatalf("want valid, got %v", err)
			}
			if !c.Valid && !errors.Is(err, ErrBadURL) {
				t.Fatalf("want ErrBadURL, got %v", err)
			}
			if err != nil && in != "" && strings.Contains(err.Error(), in) {
				t.Fatal("the error holds the input")
			}
		})
	}
}

func TestValidateCodeCases(t *testing.T) {
	for _, c := range loadCases(t).Codes {
		t.Run(c.Name, func(t *testing.T) {
			in := expand(c.Input, c.PadTo)
			got, err := ValidateCode(in)
			if c.Valid {
				if err != nil {
					t.Fatalf("want valid, got %v", err)
				}
				want := c.Trimmed
				if want == "" {
					want = in
				}
				if got != want {
					t.Fatalf("trimmed = %q, want %q", got, want)
				}
				return
			}
			if !errors.Is(err, ErrBadCode) {
				t.Fatalf("want ErrBadCode, got %v", err)
			}
			if got != "" {
				t.Fatal("an invalid code returned text")
			}
			if strings.TrimSpace(in) != "" && strings.Contains(err.Error(), strings.TrimSpace(in)) {
				t.Fatal("the error holds the input")
			}
		})
	}
}

func TestLimitsMatchTheSpec(t *testing.T) {
	if MaxURLBytes != 2048 || MinCodeBytes != 8 || MaxCodeBytes != 512 {
		t.Fatal("limits changed")
	}
}
