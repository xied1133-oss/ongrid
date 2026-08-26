package marketplace

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// extractTarGz reads a gzipped tar from r and lays its files out
// under dst. Path safety: every entry is rejected if its cleaned
// path escapes dst (tarball traversal guard).
//
// Symlinks are written as symlinks but only when the link target,
// resolved against dst, also lands inside dst. Otherwise the entry
// is skipped with no error — defence in depth, since the validate
// step (LoadPluginContainer) already rejects escape via
// pathSafeUnderRoot.
//
// Hardlinks, char/block devices, fifos, and other unusual types
// are silently skipped — the marketplace is for skill bundles, not
// arbitrary archives.
func extractTarGz(r io.Reader, dst string) error {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return fmt.Errorf("gzip reader: %w", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)

	cleanDst, err := filepath.Abs(dst)
	if err != nil {
		return err
	}

	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("tar next: %w", err)
		}
		// Skip absolute / parent-component paths up front.
		if filepath.IsAbs(hdr.Name) || strings.Contains(hdr.Name, "..") {
			continue
		}
		target := filepath.Join(cleanDst, hdr.Name)
		// Re-resolve to catch sneaky `./..` patterns the cleaner
		// might have folded.
		clean, err := filepath.Abs(target)
		if err != nil {
			continue
		}
		if !strings.HasPrefix(clean, cleanDst+string(filepath.Separator)) && clean != cleanDst {
			continue
		}

		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(clean, fs(hdr.Mode, 0o755)); err != nil {
				return err
			}
		case tar.TypeReg, tar.TypeRegA:
			if err := os.MkdirAll(filepath.Dir(clean), 0o755); err != nil {
				return err
			}
			out, err := os.OpenFile(clean, os.O_RDWR|os.O_CREATE|os.O_TRUNC, fs(hdr.Mode, 0o644))
			if err != nil {
				return err
			}
			// Cap per-file size at 32MB — skill bundles are tiny;
			// anything larger is almost certainly a bug or attack.
			if _, err := io.CopyN(out, tr, 32<<20); err != nil && err != io.EOF {
				out.Close()
				return fmt.Errorf("write %s: %w", hdr.Name, err)
			}
			out.Close()
		case tar.TypeSymlink:
			// Resolve the symlink target relative to clean's parent
			// dir; only honour it when it stays inside cleanDst.
			linkBase := filepath.Dir(clean)
			abs := filepath.Join(linkBase, hdr.Linkname)
			absClean, err := filepath.Abs(abs)
			if err != nil {
				continue
			}
			if !strings.HasPrefix(absClean, cleanDst+string(filepath.Separator)) && absClean != cleanDst {
				continue
			}
			if err := os.MkdirAll(filepath.Dir(clean), 0o755); err != nil {
				return err
			}
			_ = os.Symlink(hdr.Linkname, clean)
		default:
			// hardlinks / devices / fifos — skip
			continue
		}
	}
}

// fs picks a sane mode bit set: explicit mode if non-zero, else fallback.
func fs(mode int64, fallback os.FileMode) os.FileMode {
	if mode == 0 {
		return fallback
	}
	return os.FileMode(mode) & 0o777
}
