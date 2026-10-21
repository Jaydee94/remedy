package runner

import (
	"bytes"
	"testing"
)

// TestAnUndeliveredHeadURLIsZeroed is the case in which the child's exit won the first select: the head result with a
// valid URL stays in its buffered channel, and dropHead zeroes it.
func TestAnUndeliveredHeadURLIsZeroed(t *testing.T) {
	const url = "https://claude.com/cai/oauth/authorize?code=true&client_id=fake&response_type=code&code_challenge=fake&state=fake"
	o := newCLIOutput()
	head := append(append(append([]byte{}, openingLine...), '\n'), visitPrefix...)
	o.write(0, append(append(head, url...), '\n'))

	h := <-o.head
	if h.reason != "" || string(h.url.Bytes()) != url {
		t.Fatalf("the head was not judged a valid URL: reason %q", h.reason)
	}
	o.head <- h // back in the channel, as if drive never received it
	o.dropHead()
	if b := h.url.Bytes(); len(b) != len(url) || bytes.Count(b, []byte{0}) != len(b) {
		t.Fatal("the undelivered URL was not zeroed")
	}
	o.dropHead() // an empty channel does not block
}
