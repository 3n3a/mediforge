package archive

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// Sidecar describes an auxiliary file (typically an external SRT) that
// should be atomically placed alongside the encoded output during
// SafeReplaceWithSidecars. TmpPath must live in the same directory as
// FinalPath so renames stay on one filesystem.
type Sidecar struct {
	TmpPath   string
	FinalPath string
}

// SafeReplace swaps src with the freshly encoded file at encodedTmp.
// encodedTmp must live in the same directory as src (same filesystem).
// Equivalent to SafeReplaceWithSidecars(src, encodedTmp, nil).
func SafeReplace(src, encodedTmp string) (string, error) {
	return SafeReplaceWithSidecars(src, encodedTmp, nil)
}

// SafeReplaceWithSidecars swaps src with encodedTmp and atomically places
// any provided sidecars (e.g. external SRTs) beside it.
//
// Steps (ordering prevents data loss on same-filename cases such as a .mp4
// source encoded to a .mp4 output):
//  1. rename src -> sidecar ("<dir>/.<basename>.original")
//  2. rename encodedTmp -> finalTarget ("<dir>/<stem>.mp4")
//  3. rename each Sidecar.TmpPath -> Sidecar.FinalPath
//  4. fsync the directory so the renames are durable
//  5. remove the .original sidecar
//
// If step 2 fails, we attempt to restore the original by renaming the
// .original sidecar back to src.
// If step 3 fails partway through, we unroll: rename already-moved sidecars
// back to their TmpPath, then restore the original.
//
// Returns the final target path on success.
func SafeReplaceWithSidecars(src, encodedTmp string, sidecars []Sidecar) (string, error) {
	dir := filepath.Dir(src)
	base := filepath.Base(src)
	originalSidecar := filepath.Join(dir, "."+base+".original")

	stem := base
	if dot := strings.LastIndexByte(base, '.'); dot > 0 {
		stem = base[:dot]
	}
	finalTarget := filepath.Join(dir, stem+".mp4")

	if err := os.Rename(src, originalSidecar); err != nil {
		return "", fmt.Errorf("move original to sidecar: %w", err)
	}

	if err := os.Rename(encodedTmp, finalTarget); err != nil {
		// Try to restore the original.
		if rerr := os.Rename(originalSidecar, src); rerr != nil {
			return "", fmt.Errorf("rename encoded to final: %w (restore failed: %v; sidecar at %s)", err, rerr, originalSidecar)
		}
		return "", fmt.Errorf("rename encoded to final: %w (original restored)", err)
	}

	// Place sidecars, tracking progress so we can unroll on failure.
	placed := make([]Sidecar, 0, len(sidecars))
	for _, s := range sidecars {
		if err := os.Rename(s.TmpPath, s.FinalPath); err != nil {
			// Unroll: sidecars already moved -> back to tmp.
			for _, p := range placed {
				_ = os.Rename(p.FinalPath, p.TmpPath)
			}
			// Undo encoded rename and restore original.
			if uerr := os.Rename(finalTarget, encodedTmp); uerr != nil {
				return "", fmt.Errorf("place sidecar %s: %w (unroll encoded failed: %v)", s.FinalPath, err, uerr)
			}
			if rerr := os.Rename(originalSidecar, src); rerr != nil {
				return "", fmt.Errorf("place sidecar %s: %w (restore original failed: %v; sidecar at %s)", s.FinalPath, err, rerr, originalSidecar)
			}
			return "", fmt.Errorf("place sidecar %s: %w (original restored)", s.FinalPath, err)
		}
		placed = append(placed, s)
	}

	if err := fsyncDir(dir); err != nil {
		// Non-fatal; the renames were accepted by the filesystem, we just
		// couldn't force a sync. Proceed with sidecar removal.
		_ = err
	}

	if err := os.Remove(originalSidecar); err != nil && !os.IsNotExist(err) {
		return finalTarget, fmt.Errorf("remove sidecar %s: %w", originalSidecar, err)
	}

	return finalTarget, nil
}

// TmpPathFor returns the temp path used for downloading the encoded file
// beside the source. Also the path removed on dispatch cleanup.
func TmpPathFor(src string) string {
	dir := filepath.Dir(src)
	base := filepath.Base(src)
	return filepath.Join(dir, "."+base+".mediforge.tmp")
}

func fsyncDir(dir string) error {
	f, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}

// SafeArchiveWithSidecars moves src to archiveTarget (the original is preserved,
// not deleted), renames encodedTmp into place beside the original source, and
// atomically places any sidecars. encodedTmp must live in the same directory
// as src. archiveTarget may be on a different filesystem — cross-device moves
// fall back to copy+fsync+unlink.
//
// Collision handling against archiveTarget:
//   - identical file already there (size+mtime match): skip the move and just
//     remove src, preserving the existing archived copy.
//   - different file already there: the caller's archiveTarget is rewritten
//     with an auto-suffix (foo.mkv -> foo.1.mkv) until an unused name is found.
//
// Returns the final target path on success.
func SafeArchiveWithSidecars(src, encodedTmp, archiveTarget string, sidecars []Sidecar) (string, error) {
	dir := filepath.Dir(src)
	base := filepath.Base(src)

	stem := base
	if dot := strings.LastIndexByte(base, '.'); dot > 0 {
		stem = base[:dot]
	}
	finalTarget := filepath.Join(dir, stem+".mp4")

	if err := os.MkdirAll(filepath.Dir(archiveTarget), 0o755); err != nil {
		return "", fmt.Errorf("mkdir archive parent: %w", err)
	}

	resolvedArchive, alreadyThere, err := resolveArchiveTarget(src, archiveTarget)
	if err != nil {
		return "", fmt.Errorf("resolve archive target: %w", err)
	}

	archivedByMove := false
	if alreadyThere {
		if err := os.Remove(src); err != nil {
			return "", fmt.Errorf("remove src (archive already has identical copy at %s): %w", resolvedArchive, err)
		}
	} else {
		if err := moveFile(src, resolvedArchive); err != nil {
			return "", fmt.Errorf("move src to archive %s: %w", resolvedArchive, err)
		}
		archivedByMove = true
	}

	if err := os.Rename(encodedTmp, finalTarget); err != nil {
		if archivedByMove {
			if rerr := moveFile(resolvedArchive, src); rerr != nil {
				return "", fmt.Errorf("rename encoded to final: %w (restore from archive failed: %v; original at %s)", err, rerr, resolvedArchive)
			}
		}
		return "", fmt.Errorf("rename encoded to final: %w", err)
	}

	placed := make([]Sidecar, 0, len(sidecars))
	for _, s := range sidecars {
		if err := os.Rename(s.TmpPath, s.FinalPath); err != nil {
			for _, p := range placed {
				_ = os.Rename(p.FinalPath, p.TmpPath)
			}
			if uerr := os.Rename(finalTarget, encodedTmp); uerr != nil {
				return "", fmt.Errorf("place sidecar %s: %w (unroll encoded failed: %v)", s.FinalPath, err, uerr)
			}
			if archivedByMove {
				if rerr := moveFile(resolvedArchive, src); rerr != nil {
					return "", fmt.Errorf("place sidecar %s: %w (restore from archive failed: %v; original at %s)", s.FinalPath, err, rerr, resolvedArchive)
				}
			}
			return "", fmt.Errorf("place sidecar %s: %w", s.FinalPath, err)
		}
		placed = append(placed, s)
	}

	_ = fsyncDir(dir)

	return finalTarget, nil
}

// resolveArchiveTarget returns the target path under ARCHIVE_DIR that src
// should land at.
//
//   - If nothing exists at desired: returns (desired, false, nil).
//   - If the file at desired has the same (size, mtime) as src: returns
//     (desired, true, nil) — caller should skip the move and just delete src.
//   - Otherwise: scans desired.1.ext, desired.2.ext, ... for the first name
//     that is free or holds an identical copy.
func resolveArchiveTarget(src, desired string) (string, bool, error) {
	srcInfo, err := os.Stat(src)
	if err != nil {
		return "", false, fmt.Errorf("stat src: %w", err)
	}

	same, existed, err := matchesSrc(desired, srcInfo)
	if err != nil {
		return "", false, err
	}
	if !existed {
		return desired, false, nil
	}
	if same {
		return desired, true, nil
	}

	dir := filepath.Dir(desired)
	base := filepath.Base(desired)
	stem := base
	ext := ""
	if dot := strings.LastIndexByte(base, '.'); dot > 0 {
		stem = base[:dot]
		ext = base[dot:]
	}

	for i := 1; i < 10000; i++ {
		candidate := filepath.Join(dir, fmt.Sprintf("%s.%d%s", stem, i, ext))
		same, existed, err := matchesSrc(candidate, srcInfo)
		if err != nil {
			return "", false, err
		}
		if !existed {
			return candidate, false, nil
		}
		if same {
			return candidate, true, nil
		}
	}
	return "", false, fmt.Errorf("no free archive suffix found for %s", desired)
}

// matchesSrc reports whether path exists and, if so, whether its size and
// mtime match srcInfo. existed=false means path was absent.
func matchesSrc(path string, srcInfo os.FileInfo) (same, existed bool, err error) {
	st, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, false, nil
		}
		return false, false, fmt.Errorf("stat %s: %w", path, err)
	}
	if st.IsDir() {
		return false, true, fmt.Errorf("archive target %s is a directory", path)
	}
	same = st.Size() == srcInfo.Size() && st.ModTime().Unix() == srcInfo.ModTime().Unix()
	return same, true, nil
}

// moveFile renames src to dst, falling back to a copy + fsync + unlink on
// cross-device (EXDEV) errors. dst's parent directory must already exist.
func moveFile(src, dst string) error {
	if err := os.Rename(src, dst); err == nil {
		return nil
	} else if !isCrossDevice(err) {
		return err
	}
	return copyAndUnlink(src, dst)
}

func isCrossDevice(err error) bool {
	var linkErr *os.LinkError
	if errors.As(err, &linkErr) {
		return errors.Is(linkErr.Err, syscall.EXDEV)
	}
	return errors.Is(err, syscall.EXDEV)
}

func copyAndUnlink(src, dst string) error {
	srcInfo, err := os.Stat(src)
	if err != nil {
		return fmt.Errorf("stat src: %w", err)
	}

	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("open src: %w", err)
	}
	defer in.Close()

	tmp := filepath.Join(filepath.Dir(dst), "."+filepath.Base(dst)+".mediforge.archiving.tmp")
	out, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return fmt.Errorf("open tmp: %w", err)
	}
	cleanupTmp := func() { _ = os.Remove(tmp) }

	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		cleanupTmp()
		return fmt.Errorf("copy: %w", err)
	}
	if err := out.Sync(); err != nil {
		_ = out.Close()
		cleanupTmp()
		return fmt.Errorf("fsync tmp: %w", err)
	}
	if err := out.Close(); err != nil {
		cleanupTmp()
		return fmt.Errorf("close tmp: %w", err)
	}
	if err := os.Chtimes(tmp, srcInfo.ModTime(), srcInfo.ModTime()); err != nil {
		cleanupTmp()
		return fmt.Errorf("chtimes tmp: %w", err)
	}
	if err := os.Rename(tmp, dst); err != nil {
		cleanupTmp()
		return fmt.Errorf("rename tmp to dst: %w", err)
	}
	_ = fsyncDir(filepath.Dir(dst))
	if err := os.Remove(src); err != nil {
		return fmt.Errorf("remove src after copy (dst at %s): %w", dst, err)
	}
	return nil
}
