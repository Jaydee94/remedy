// Package cliinstall installs the pinned agent CLI into a directory: it downloads one artifact over HTTPS, checks its
// SHA-256 and only then writes the executable. The runner image does not contain the CLI (the images are public and the
// binary is proprietary), so an init container of the runner pod runs this before the runner starts.
//
// The package never executes what it downloaded and never touches the CLI's login: it writes one file, and it writes
// it only when the checksum matches.
package cliinstall

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// maxDownload bounds the artifact. The CLI is a few hundred megabytes at most.
const maxDownload = 512 << 20

var (
	versionRE  = regexp.MustCompile(`^[0-9A-Za-z][0-9A-Za-z._-]{0,63}$`)
	platformRE = regexp.MustCompile(`^[0-9A-Za-z][0-9A-Za-z._-]{0,63}$`)
	sha256RE   = regexp.MustCompile(`^[0-9a-f]{64}$`)
	nameRE     = regexp.MustCompile(`^[0-9A-Za-z][0-9A-Za-z._-]{0,63}$`)
)

// Platform is the vendor's name for an architecture and the SHA-256 of the artifact for it.
type Platform struct {
	Name   string
	SHA256 string
}

// Options says what to install.
type Options struct {
	URLTemplate string              // an https URL with {version} and {platform}
	Version     string              // the pinned version
	Platforms   map[string]Platform // by GOARCH: "amd64", "arm64"
	GOARCH      string              // the architecture to install for; the command passes runtime.GOARCH
	Dest        string              // the directory to install into; it must exist
	Name        string              // the file name of the installed executable
	Archive     string              // "none": the artifact is the executable; "tar.gz": it holds it as Member
	Member      string              // for "tar.gz": the base name of the file to take
	HTTP        *http.Client        // nil: a client with a timeout; either way only https redirects are followed
}

func (o Options) validate() (Platform, error) {
	if !versionRE.MatchString(o.Version) {
		return Platform{}, fmt.Errorf("the version %q is not a version string", o.Version)
	}
	if !nameRE.MatchString(o.Name) {
		return Platform{}, fmt.Errorf("the file name %q is not a plain name", o.Name)
	}
	switch o.Archive {
	case "none":
	case "tar.gz":
		if !nameRE.MatchString(o.Member) {
			return Platform{}, fmt.Errorf("the archive member %q is not a plain name", o.Member)
		}
	default:
		return Platform{}, fmt.Errorf("the archive type %q is not none or tar.gz", o.Archive)
	}
	p, ok := o.Platforms[o.GOARCH]
	if !ok {
		return Platform{}, fmt.Errorf("no platform is configured for the architecture %q", o.GOARCH)
	}
	if !platformRE.MatchString(p.Name) {
		return Platform{}, fmt.Errorf("the platform name %q is not a plain name", p.Name)
	}
	if !sha256RE.MatchString(p.SHA256) {
		return Platform{}, fmt.Errorf("the checksum for %s is not 64 lower-case hex digits", o.GOARCH)
	}
	if info, err := os.Stat(o.Dest); err != nil || !info.IsDir() {
		return Platform{}, fmt.Errorf("the destination %q is not a directory", o.Dest)
	}
	return p, nil
}

// downloadURL fills the template and refuses anything but https.
func (o Options) downloadURL(p Platform) (string, error) {
	raw := strings.NewReplacer("{version}", o.Version, "{platform}", p.Name).Replace(o.URLTemplate)
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return "", errors.New("the download URL must be an https URL")
	}
	return raw, nil
}

func (o Options) client() *http.Client {
	var c http.Client
	if o.HTTP != nil {
		c = *o.HTTP
	} else {
		c.Timeout = 10 * time.Minute
	}
	// The https-only rule also holds after a redirect, whatever client was injected.
	inner := c.CheckRedirect
	c.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if req.URL.Scheme != "https" {
			return errors.New("a redirect to a URL that is not https")
		}
		if inner != nil {
			return inner(req, via)
		}
		if len(via) >= 5 {
			return errors.New("too many redirects")
		}
		return nil
	}
	return &c
}

// Install downloads, verifies and installs the CLI and returns the path of the executable. On any failure nothing is
// left in the destination.
func Install(ctx context.Context, o Options) (string, error) {
	p, err := o.validate()
	if err != nil {
		return "", err
	}
	target, err := o.downloadURL(p)
	if err != nil {
		return "", err
	}

	download, err := os.CreateTemp(o.Dest, ".download-*")
	if err != nil {
		return "", err
	}
	defer os.Remove(download.Name())
	defer download.Close()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return "", err
	}
	resp, err := o.client().Do(req)
	if err != nil {
		return "", fmt.Errorf("download: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download: HTTP %d", resp.StatusCode)
	}
	hash := sha256.New()
	n, err := io.Copy(io.MultiWriter(download, hash), io.LimitReader(resp.Body, maxDownload+1))
	if err != nil {
		return "", fmt.Errorf("download: %w", err)
	}
	if n > maxDownload {
		return "", errors.New("download: the artifact is larger than the installer reads")
	}
	if n == 0 {
		return "", errors.New("download: the artifact is empty: refusing to install a file with no content")
	}
	if got := hex.EncodeToString(hash.Sum(nil)); got != p.SHA256 {
		return "", fmt.Errorf("the checksum of the download is %s, pinned is %s: refusing to install it", got, p.SHA256)
	}

	staged, err := os.CreateTemp(o.Dest, ".install-*")
	if err != nil {
		return "", err
	}
	defer os.Remove(staged.Name())
	defer staged.Close()
	if _, err := download.Seek(0, io.SeekStart); err != nil {
		return "", err
	}
	switch o.Archive {
	case "none":
		_, err = io.Copy(staged, download)
	case "tar.gz":
		err = extract(download, o.Member, staged)
	}
	if err != nil {
		return "", err
	}
	if err := staged.Chmod(0o755); err != nil {
		return "", err
	}
	if err := staged.Close(); err != nil {
		return "", err
	}
	final := filepath.Join(o.Dest, o.Name)
	if err := os.Rename(staged.Name(), final); err != nil {
		return "", err
	}
	return final, nil
}

// extract copies the one regular file whose base name is member out of a tar.gz. Every other entry is skipped, and no
// path in the archive is ever joined to a destination, so an entry cannot write anywhere.
func extract(r io.Reader, member string, w io.Writer) error {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return fmt.Errorf("the archive: %w", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return fmt.Errorf("the archive has no file named %q", member)
		}
		if err != nil {
			return fmt.Errorf("the archive: %w", err)
		}
		if hdr.Typeflag != tar.TypeReg || path.Base(hdr.Name) != member {
			continue
		}
		n, err := io.Copy(w, io.LimitReader(tr, maxDownload+1))
		if err != nil {
			return fmt.Errorf("the archive: %w", err)
		}
		if n > maxDownload {
			return errors.New("the archive member is larger than the installer reads")
		}
		if n == 0 {
			return fmt.Errorf("the archive member %q is empty", member)
		}
		return nil
	}
}
