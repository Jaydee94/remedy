package sshhost_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/Jaydee94/remedy/internal/sshhost"
)

func valid() sshhost.Fields {
	return sshhost.Fields{Name: "nas", Address: "192.168.178.118", Port: 22, Account: "remedy"}
}

func TestValidateAcceptsGoodFields(t *testing.T) {
	for name, mutate := range map[string]func(f *sshhost.Fields){
		"plain":           func(f *sshhost.Fields) {},
		"a host name":     func(f *sshhost.Fields) { f.Address = "nas.homeserver" },
		"upper case":      func(f *sshhost.Fields) { f.Address = "NAS.Example.com"; f.Name = "Nas-1" },
		"an IPv6 literal": func(f *sshhost.Fields) { f.Address = "fe80::1" },
		"notes":           func(f *sshhost.Fields) { f.Notes = "Paperless lives here.\nSecond line." },
		"docker":          func(f *sshhost.Fields) { f.DockerPattern = "remedy-*" },
	} {
		t.Run(name, func(t *testing.T) {
			f := valid()
			mutate(&f)
			if err := f.Validate(); err != nil {
				t.Fatalf("Validate: %v", err)
			}
		})
	}
}

func TestValidateRejects(t *testing.T) {
	for name, mutate := range map[string]func(f *sshhost.Fields){
		"empty name":      func(f *sshhost.Fields) { f.Name = "" },
		"name with space": func(f *sshhost.Fields) { f.Name = "my nas" },
		"long name":       func(f *sshhost.Fields) { f.Name = strings.Repeat("a", 41) },
		"newline name":    func(f *sshhost.Fields) { f.Name = "nas\n" },
		"empty address":   func(f *sshhost.Fields) { f.Address = "" },
		"address option":  func(f *sshhost.Fields) { f.Address = "-oProxyCommand=x" },
		"address spaces":  func(f *sshhost.Fields) { f.Address = "a b" },
		"port 0":          func(f *sshhost.Fields) { f.Port = 0 },
		"port 70000":      func(f *sshhost.Fields) { f.Port = 70000 },
		"account":         func(f *sshhost.Fields) { f.Account = "Root" },
		"long notes":      func(f *sshhost.Fields) { f.Notes = strings.Repeat("x", 2001) },
		"nul in notes":    func(f *sshhost.Fields) { f.Notes = "a\x00b" },
		"docker":          func(f *sshhost.Fields) { f.DockerPattern = "a b" },
		"unit":            func(f *sshhost.Fields) { f.SystemdUnits = []string{"a;b"} },
		"unit with colon": func(f *sshhost.Fields) { f.SystemdUnits = []string{"x:y"} },
	} {
		t.Run(name, func(t *testing.T) {
			f := valid()
			mutate(&f)
			err := f.Validate()
			var fe *sshhost.FieldError
			if !errors.As(err, &fe) || fe.Msg == "" {
				t.Fatalf("Validate = %v, want a FieldError", err)
			}
		})
	}
}

func TestValidateNormalisesUnits(t *testing.T) {
	f := valid()
	f.SystemdUnits = []string{" node_exporter ", "backup.timer", "node_exporter.service", "", "x.socket"}
	if err := f.Validate(); err != nil {
		t.Fatal(err)
	}
	want := []string{"node_exporter.service", "backup.timer", "x.socket"}
	if !reflect.DeepEqual(f.SystemdUnits, want) {
		t.Fatalf("units = %v, want %v", f.SystemdUnits, want)
	}
}
