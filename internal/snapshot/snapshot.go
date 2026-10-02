// Package snapshot moves a repository snapshot from the control plane to the runner. The control plane
// filters GitHub's tarball while it streams it (Filter); the runner unpacks it and trusts nothing (Unpack).
package snapshot

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
)

const (
	// MaxBytes is the most file content a snapshot may have (spec section 6).
	MaxBytes = 50 << 20
	// MaxFiles is the most entries a snapshot may have.
	MaxFiles = 20000
)

var (
	// ErrTooLarge means the snapshot has more content or more entries than the limits allow.
	ErrTooLarge = errors.New("snapshot: too large")
	// ErrUnsafe means an entry would be written outside the target directory.
	ErrUnsafe = errors.New("snapshot: unsafe archive entry")
)

// Limits bounds a snapshot. A zero field means the default.
type Limits struct {
	MaxBytes int64
	MaxFiles int
}

func (l Limits) withDefaults() Limits {
	if l.MaxBytes <= 0 {
		l.MaxBytes = MaxBytes
	}
	if l.MaxFiles <= 0 {
		l.MaxFiles = MaxFiles
	}
	return l
}

// IsSecret reports whether a path names a file that is left out of a snapshot: .env*, *.pem, *.key and
// id_rsa*. Only the file name counts, and case does not.
func IsSecret(name string) bool {
	b := strings.ToLower(path.Base(name))
	return strings.HasPrefix(b, ".env") || strings.HasSuffix(b, ".pem") || strings.HasSuffix(b, ".key") || strings.HasPrefix(b, "id_rsa")
}

// split checks an entry name and returns its parts below the top-level directory that GitHub's archives
// have. root is true for the top-level directory itself.
func split(name string) (rel []string, root bool, err error) {
	if strings.ContainsRune(name, 0) || strings.HasPrefix(name, "/") {
		return nil, false, fmt.Errorf("%w: %q", ErrUnsafe, name)
	}
	var parts []string
	for _, p := range strings.Split(name, "/") {
		switch p {
		case "", ".":
		case "..":
			return nil, false, fmt.Errorf("%w: %q", ErrUnsafe, name)
		default:
			parts = append(parts, p)
		}
	}
	if len(parts) <= 1 {
		return nil, true, nil
	}
	return parts[1:], false, nil
}

// safeLink reports whether a symlink target can only lead downwards from the link's own directory:
// relative, not empty and without a ".." component. Anything cleverer is not safe to judge lexically.
func safeLink(target string) bool {
	if target == "" || strings.HasPrefix(target, "/") || strings.ContainsRune(target, 0) {
		return false
	}
	for _, p := range strings.Split(target, "/") {
		if p == ".." {
			return false
		}
	}
	return true
}

// Filter copies a gzipped tar from src to dst without the files IsSecret names, without hard links,
// devices and the pax global header, and enforces the limits. Symlinks pass; Unpack judges them.
func Filter(dst io.Writer, src io.Reader, lim Limits) error {
	lim = lim.withDefaults()
	zr, err := gzip.NewReader(src)
	if err != nil {
		return fmt.Errorf("snapshot: not a gzip stream: %w", err)
	}
	defer zr.Close()
	tr := tar.NewReader(zr)
	zw := gzip.NewWriter(dst)
	tw := tar.NewWriter(zw)

	entries, total := 0, int64(0)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("snapshot: %w", err)
		}
		if entries++; entries > lim.MaxFiles {
			return fmt.Errorf("%w: more than %d entries", ErrTooLarge, lim.MaxFiles)
		}
		if _, _, err := split(h.Name); err != nil {
			return err
		}

		out := &tar.Header{Name: h.Name, Mode: h.Mode, ModTime: h.ModTime, Typeflag: h.Typeflag}
		switch h.Typeflag {
		case tar.TypeDir:
			if err := tw.WriteHeader(out); err != nil {
				return err
			}
		case tar.TypeSymlink:
			out.Linkname = h.Linkname
			if err := tw.WriteHeader(out); err != nil {
				return err
			}
		case tar.TypeReg:
			if IsSecret(h.Name) {
				continue
			}
			if total += h.Size; total > lim.MaxBytes {
				return fmt.Errorf("%w: more than %d bytes", ErrTooLarge, lim.MaxBytes)
			}
			out.Size = h.Size
			if err := tw.WriteHeader(out); err != nil {
				return err
			}
			if _, err := io.CopyN(tw, tr, h.Size); err != nil {
				return fmt.Errorf("snapshot: %w", err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		return err
	}
	return zw.Close()
}

// Result describes what Unpack extracted. Skipped lists the symlinks and special files it left out.
type Result struct {
	Files   int
	Bytes   int64
	Skipped []string
}

// Unpack extracts a gzipped tar into dir, which must exist, below its top-level directory. It returns
// ErrUnsafe for an entry that would leave dir and ErrTooLarge beyond the limits. After an error dir may
// hold part of the snapshot; the caller deletes it.
func Unpack(dir string, src io.Reader, lim Limits) (Result, error) {
	lim = lim.withDefaults()
	root, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return Result{}, fmt.Errorf("snapshot: %w", err)
	}
	zr, err := gzip.NewReader(src)
	if err != nil {
		return Result{}, fmt.Errorf("snapshot: not a gzip stream: %w", err)
	}
	defer zr.Close()
	tr := tar.NewReader(zr)

	var res Result
	entries := 0
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return res, nil
		}
		if err != nil {
			return res, fmt.Errorf("snapshot: %w", err)
		}
		if entries++; entries > lim.MaxFiles {
			return res, fmt.Errorf("%w: more than %d entries", ErrTooLarge, lim.MaxFiles)
		}
		parts, isRoot, err := split(h.Name)
		if err != nil {
			return res, err
		}
		if h.Typeflag == tar.TypeXGlobalHeader || isRoot {
			continue
		}
		rel := path.Join(parts...)
		target := filepath.Join(root, filepath.FromSlash(rel))

		switch h.Typeflag {
		case tar.TypeDir:
			if err := makeDir(root, target); err != nil {
				return res, err
			}
		case tar.TypeReg:
			if IsSecret(rel) {
				continue
			}
			if res.Bytes += h.Size; res.Bytes > lim.MaxBytes {
				return res, fmt.Errorf("%w: more than %d bytes", ErrTooLarge, lim.MaxBytes)
			}
			if err := makeDir(root, filepath.Dir(target)); err != nil {
				return res, err
			}
			perm := os.FileMode(0o600)
			if h.Mode&0o100 != 0 {
				perm = 0o700
			}
			f, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, perm)
			if err != nil {
				return res, fmt.Errorf("snapshot: %w", err)
			}
			_, err = io.CopyN(f, tr, h.Size)
			if cerr := f.Close(); err == nil {
				err = cerr
			}
			if err != nil {
				return res, fmt.Errorf("snapshot: %w", err)
			}
			res.Files++
		case tar.TypeSymlink:
			if !safeLink(h.Linkname) {
				res.Skipped = append(res.Skipped, rel)
				continue
			}
			if err := makeDir(root, filepath.Dir(target)); err != nil {
				return res, err
			}
			if err := os.Symlink(h.Linkname, target); err != nil {
				return res, fmt.Errorf("snapshot: %w", err)
			}
		default:
			res.Skipped = append(res.Skipped, rel)
		}
	}
}

// makeDir creates dir and its parents and checks that the real path, after following symlinks that are
// already there, is still inside root.
func makeDir(root, dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("snapshot: %w", err)
	}
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return fmt.Errorf("snapshot: %w", err)
	}
	if real != root && !strings.HasPrefix(real, root+string(os.PathSeparator)) {
		return fmt.Errorf("%w: %q leads out of the workspace", ErrUnsafe, dir)
	}
	return nil
}
