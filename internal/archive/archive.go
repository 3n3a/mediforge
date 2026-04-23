package archive

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
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
