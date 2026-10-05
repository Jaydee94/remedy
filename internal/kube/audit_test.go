package kube

import (
	"reflect"
	"slices"
	"strings"
	"testing"
)

// The read side can only ever read: every exported method of the Reader is a Get or a List. This is the same
// rule as for the GitHub client; the transport is the second line of defence.
func TestEveryExportedMethodOfTheReaderIsAGetOrAList(t *testing.T) {
	typ := reflect.TypeOf(&Reader{})
	for i := 0; i < typ.NumMethod(); i++ {
		name := typ.Method(i).Name
		if !strings.HasPrefix(name, "Get") && !strings.HasPrefix(name, "List") {
			t.Errorf("Reader.%s is neither a Get nor a List: the read side must not change anything", name)
		}
	}
}

// The write side can do four things and nothing else. Adding a fifth is a decision that belongs into the spec.
func TestTheWriterHasExactlyTheFourActionsOfTheSpec(t *testing.T) {
	typ := reflect.TypeOf(&Writer{})
	var got []string
	for i := 0; i < typ.NumMethod(); i++ {
		got = append(got, typ.Method(i).Name)
	}
	slices.Sort(got)
	want := []string{"DeletePod", "RefreshApplication", "RestartWorkload", "SyncApplication"}
	if !slices.Equal(got, want) {
		t.Fatalf("Writer methods = %v, want %v", got, want)
	}
}
