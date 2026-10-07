package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/Jaydee94/remedy/internal/cliinstall"
)

// platformFlag collects `--platform GOARCH=NAME@SHA256` flags: Go's name for an architecture, the vendor's name for the
// platform, and the SHA-256 of the artifact.
type platformFlag struct {
	m map[string]cliinstall.Platform
}

func (p *platformFlag) String() string { return fmt.Sprint(p.m) }

func (p *platformFlag) Set(v string) error {
	arch, rest, ok := strings.Cut(v, "=")
	name, sum, ok2 := strings.Cut(rest, "@")
	if !ok || !ok2 || arch == "" || name == "" || sum == "" || strings.ContainsAny(name, "=@") || strings.Contains(sum, "@") {
		return fmt.Errorf("%q is not GOARCH=NAME@SHA256", v)
	}
	if p.m == nil {
		p.m = map[string]cliinstall.Platform{}
	}
	if _, dup := p.m[arch]; dup {
		return fmt.Errorf("the architecture %s is given twice", arch)
	}
	p.m[arch] = cliinstall.Platform{Name: name, SHA256: sum}
	return nil
}

func (p *platformFlag) platforms() map[string]cliinstall.Platform { return p.m }

// installCLI is `remedy-runner install-cli`, the command of the init container of the runner pod. It returns the exit
// code: 0 on success, 1 when the install failed, 2 for a wrong command line.
func installCLI(args []string) int {
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	fs := flag.NewFlagSet("install-cli", flag.ContinueOnError)
	var platforms platformFlag
	version := fs.String("version", "", "the pinned CLI version (required)")
	template := fs.String("url-template", "", "an https URL with {version} and {platform} (required)")
	dest := fs.String("dest", "/opt/claude", "the directory to install into")
	name := fs.String("name", "claude", "the file name of the installed executable")
	archive := fs.String("archive", "none", "none: the download is the executable; tar.gz: it holds it as --member")
	member := fs.String("member", "", "for tar.gz: the base name of the file to take")
	fs.Var(&platforms, "platform", "GOARCH=NAME@SHA256, once per architecture (required)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *version == "" || *template == "" || len(platforms.platforms()) == 0 {
		fmt.Fprintln(os.Stderr, "install-cli needs --version, --url-template and at least one --platform")
		return 2
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	path, err := cliinstall.Install(ctx, cliinstall.Options{
		URLTemplate: *template, Version: *version, Platforms: platforms.platforms(), GOARCH: runtime.GOARCH,
		Dest: *dest, Name: *name, Archive: *archive, Member: *member,
	})
	if err != nil {
		log.Error("the CLI was not installed", "err", err)
		return 1
	}
	log.Info("installed the CLI", "path", path, "version", *version, "arch", runtime.GOARCH)
	return 0
}
