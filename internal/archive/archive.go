package archive

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// SafeReplace swaps src with the freshly encoded file at encodedTmp.
// encodedTmp must live in the same directory as src (same filesystem).
//
// Steps (ordering prevents data loss on same-filename cases such as a .mp4
// source encoded to a .mp4 output):
//  1. rename src -> sidecar ("<dir>/.<basename>.original")
//  2. rename encodedTmp -> finalTarget ("<dir>/<stem>.mp4")
//  3. fsync the directory so the rename is durable
//  4. remove the sidecar
//
// If step 2 fails, we attempt to restore the original by renaming the sidecar
// back to src.
//
// Returns the final target path on success.
func SafeReplace(src, encodedTmp string) (string, error) {
	dir := filepath.Dir(src)
	base := filepath.Base(src)
	sidecar := filepath.Join(dir, "."+base+".original")

	stem := base
	if dot := strings.LastIndexByte(base, '.'); dot > 0 {
		stem = base[:dot]
	}
	finalTarget := filepath.Join(dir, stem+".mp4")

	if err := os.Rename(src, sidecar); err != nil {
		return "", fmt.Errorf("move original to sidecar: %w", err)
	}

	if err := os.Rename(encodedTmp, finalTarget); err != nil {
		// Try to restore the original.
		if rerr := os.Rename(sidecar, src); rerr != nil {
			return "", fmt.Errorf("rename encoded to final: %w (restore failed: %v; sidecar at %s)", err, rerr, sidecar)
		}
		return "", fmt.Errorf("rename encoded to final: %w (original restored)", err)
	}

	if err := fsyncDir(dir); err != nil {
		// Non-fatal; the rename was accepted by the filesystem, we just
		// couldn't force a sync. Proceed with sidecar removal.
		_ = err
	}

	if err := os.Remove(sidecar); err != nil && !os.IsNotExist(err) {
		return finalTarget, fmt.Errorf("remove sidecar %s: %w", sidecar, err)
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
