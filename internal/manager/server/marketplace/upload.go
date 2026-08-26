package marketplace

// upload.go — POST /v1/marketplace/upload: install a pack from a browser
// file upload (zip / tar.gz). The archive is extracted to a temp dir under a
// zip-slip guard, then handed to the SAME install path as a local-dir
// install (DetectContainer → LoadPluginContainer → move into the skills
// root). Admin-only. The temp dir is removed after install copies the pack
// into its permanent home.

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	bizmp "github.com/ongridio/ongrid/internal/manager/biz/marketplace"
	"github.com/ongridio/ongrid/internal/pkg/errs"
)

const (
	maxUploadBytes   = 64 << 20  // 64 MiB request cap
	maxExtractedFile = 128 << 20 // per-file decompressed cap (zip-bomb guard)
)

func (h *Handler) upload(w http.ResponseWriter, r *http.Request) {
	caller, ok := requireAdmin(w, r)
	if !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadBytes)
	if err := r.ParseMultipartForm(maxUploadBytes); err != nil {
		writeErr(w, errors.Join(errs.ErrInvalid, fmt.Errorf("parse upload: %w", err)))
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		writeErr(w, errors.Join(errs.ErrInvalid, fmt.Errorf("missing 'file' field: %w", err)))
		return
	}
	defer file.Close()

	// Spool the upload to a temp file so zip (which needs ReaderAt) and tar
	// share one code path.
	tmp, err := os.CreateTemp("", "ongrid-upload-*"+archiveExt(header.Filename))
	if err != nil {
		writeErr(w, err)
		return
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if _, err := io.Copy(tmp, file); err != nil {
		tmp.Close()
		writeErr(w, err)
		return
	}
	tmp.Close()

	dest, err := os.MkdirTemp("", "ongrid-pack-*")
	if err != nil {
		writeErr(w, err)
		return
	}
	defer os.RemoveAll(dest)

	if err := extractArchive(tmpPath, header.Filename, dest); err != nil {
		writeErr(w, errors.Join(errs.ErrInvalid, err))
		return
	}

	// Archives commonly wrap everything in a single top-level directory
	// (e.g. "my-skill/SKILL.md"); descend into it so the pack root is found.
	root := descendSingleDir(dest)

	res, err := h.svc.Install(r.Context(), caller, bizmp.Source{Type: "local", Path: root})
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func archiveExt(name string) string {
	n := strings.ToLower(name)
	switch {
	case strings.HasSuffix(n, ".tar.gz"):
		return ".tar.gz"
	case strings.HasSuffix(n, ".tgz"):
		return ".tgz"
	case strings.HasSuffix(n, ".zip"):
		return ".zip"
	case strings.HasSuffix(n, ".tar"):
		return ".tar"
	default:
		return filepath.Ext(name)
	}
}

func extractArchive(archivePath, filename, dest string) error {
	n := strings.ToLower(filename)
	switch {
	case strings.HasSuffix(n, ".zip"):
		return extractZip(archivePath, dest)
	case strings.HasSuffix(n, ".tar.gz"), strings.HasSuffix(n, ".tgz"):
		f, err := os.Open(archivePath)
		if err != nil {
			return err
		}
		defer f.Close()
		gz, err := gzip.NewReader(f)
		if err != nil {
			return fmt.Errorf("gzip: %w", err)
		}
		defer gz.Close()
		return extractTar(gz, dest)
	case strings.HasSuffix(n, ".tar"):
		f, err := os.Open(archivePath)
		if err != nil {
			return err
		}
		defer f.Close()
		return extractTar(f, dest)
	default:
		return fmt.Errorf("unsupported archive %q (need .zip / .tar.gz / .tgz / .tar)", filename)
	}
}

func extractZip(archivePath, dest string) error {
	zr, err := zip.OpenReader(archivePath)
	if err != nil {
		return fmt.Errorf("open zip: %w", err)
	}
	defer zr.Close()
	for _, f := range zr.File {
		target, ok := safeJoin(dest, f.Name)
		if !ok {
			return fmt.Errorf("zip entry escapes archive root: %q", f.Name)
		}
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		err = writeFile(target, rc, f.FileInfo().Mode())
		rc.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

func extractTar(r io.Reader, dest string) error {
	tr := tar.NewReader(r)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("tar: %w", err)
		}
		target, ok := safeJoin(dest, hdr.Name)
		if !ok {
			return fmt.Errorf("tar entry escapes archive root: %q", hdr.Name)
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			if err := writeFile(target, tr, os.FileMode(hdr.Mode)); err != nil {
				return err
			}
			// symlinks / devices / etc. are skipped (a skill pack is plain files)
		}
	}
}

func writeFile(target string, r io.Reader, mode os.FileMode) error {
	// Preserve the executable bit from the archive entry so a skill that ships
	// a binary (e.g. terraform) stays runnable after extraction: any entry
	// that was executable lands 0755, everything else 0644. We deliberately do
	// NOT honor setuid/setgid/sticky from an uploaded archive.
	perm := os.FileMode(0o644)
	if mode&0o111 != 0 {
		perm = 0o755
	}
	out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	defer out.Close()
	// Chmod explicitly — OpenFile honors umask and won't re-mode an existing
	// file, so the executable bit could otherwise be masked away.
	if err := out.Chmod(perm); err != nil {
		return err
	}
	_, err = io.Copy(out, io.LimitReader(r, maxExtractedFile))
	return err
}

// safeJoin joins name onto base and verifies the result stays within base
// (zip-slip guard). Returns ok=false when name would escape.
func safeJoin(base, name string) (string, bool) {
	target := filepath.Join(base, name)
	rel, err := filepath.Rel(base, target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return "", false
	}
	return target, true
}

// descendSingleDir returns dir's single child directory when dir contains
// exactly one entry and it's a directory (the common "archive wraps a top
// folder" case); otherwise returns dir unchanged.
func descendSingleDir(dir string) string {
	ents, err := os.ReadDir(dir)
	if err != nil || len(ents) != 1 || !ents[0].IsDir() {
		return dir
	}
	return filepath.Join(dir, ents[0].Name())
}
