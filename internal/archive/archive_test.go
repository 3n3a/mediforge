package archive

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeFile writes content and sets mtime, returning the path.
func writeFile(t *testing.T, path, content string, mtime time.Time) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	if err := os.Chtimes(path, mtime, mtime); err != nil {
		t.Fatalf("chtimes %s: %v", path, err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

func mustNotExist(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected %s to not exist, got err=%v", path, err)
	}
}

// ---------------- SafeReplaceWithSidecars ----------------

func TestSafeReplaceWithSidecars_HappyPath(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "movie.mkv")
	enc := filepath.Join(dir, ".movie.mkv.mediforge.tmp")
	subTmp := filepath.Join(dir, ".movie.mkv.sub.0.srt.tmp")
	subFinal := filepath.Join(dir, "movie.default.srt")

	writeFile(t, src, "original-bytes", time.Now())
	writeFile(t, enc, "encoded-bytes", time.Now())
	writeFile(t, subTmp, "1\n00:00:00,000 --> 00:00:01,000\nhello\n", time.Now())

	final, err := SafeReplaceWithSidecars(src, enc, []Sidecar{{TmpPath: subTmp, FinalPath: subFinal}})
	if err != nil {
		t.Fatalf("SafeReplaceWithSidecars: %v", err)
	}
	if final != filepath.Join(dir, "movie.mp4") {
		t.Fatalf("unexpected final: %s", final)
	}
	if got := readFile(t, final); got != "encoded-bytes" {
		t.Fatalf("final content = %q, want encoded-bytes", got)
	}
	if got := readFile(t, subFinal); !strings.Contains(got, "hello") {
		t.Fatalf("sidecar content missing: %q", got)
	}
	mustNotExist(t, src)
	mustNotExist(t, enc)
	mustNotExist(t, subTmp)
	mustNotExist(t, filepath.Join(dir, ".movie.mkv.original"))
}

func TestSafeReplaceWithSidecars_SameFilename(t *testing.T) {
	// src and encoded share the final name (.mp4 in, .mp4 out). The original
	// must go via the .original sidecar to avoid being clobbered.
	dir := t.TempDir()
	src := filepath.Join(dir, "clip.mp4")
	enc := filepath.Join(dir, ".clip.mp4.mediforge.tmp")
	writeFile(t, src, "original", time.Now())
	writeFile(t, enc, "encoded", time.Now())

	final, err := SafeReplace(src, enc)
	if err != nil {
		t.Fatalf("SafeReplace: %v", err)
	}
	if final != src {
		t.Fatalf("final = %s, want %s", final, src)
	}
	if got := readFile(t, final); got != "encoded" {
		t.Fatalf("final content = %q, want encoded", got)
	}
}

// ---------------- resolveArchiveTarget ----------------

func TestResolveArchiveTarget_NoExisting(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.mkv")
	desired := filepath.Join(dir, "archive", "src.mkv")
	writeFile(t, src, "hi", time.Now())

	got, already, err := resolveArchiveTarget(src, desired)
	if err != nil {
		t.Fatalf("resolveArchiveTarget: %v", err)
	}
	if got != desired {
		t.Fatalf("got %s, want %s", got, desired)
	}
	if already {
		t.Fatal("expected alreadyThere=false")
	}
}

func TestResolveArchiveTarget_IdenticalAlreadyThere(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.mkv")
	desired := filepath.Join(dir, "archive", "src.mkv")
	mtime := time.Unix(1_700_000_000, 0)
	writeFile(t, src, "same-bytes", mtime)
	writeFile(t, desired, "same-bytes", mtime)

	got, already, err := resolveArchiveTarget(src, desired)
	if err != nil {
		t.Fatalf("resolveArchiveTarget: %v", err)
	}
	if got != desired {
		t.Fatalf("got %s, want %s", got, desired)
	}
	if !already {
		t.Fatal("expected alreadyThere=true")
	}
}

func TestResolveArchiveTarget_DifferentFileAutoSuffix(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.mkv")
	desired := filepath.Join(dir, "archive", "src.mkv")
	writeFile(t, src, "new-content-different-size", time.Now())
	writeFile(t, desired, "old-stuff", time.Now().Add(-24*time.Hour))

	got, already, err := resolveArchiveTarget(src, desired)
	if err != nil {
		t.Fatalf("resolveArchiveTarget: %v", err)
	}
	want := filepath.Join(dir, "archive", "src.1.mkv")
	if got != want {
		t.Fatalf("got %s, want %s", got, want)
	}
	if already {
		t.Fatal("expected alreadyThere=false at suffix position")
	}
}

func TestResolveArchiveTarget_SuffixChain(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.mkv")
	writeFile(t, src, "AAAA", time.Now())
	archDir := filepath.Join(dir, "archive")
	// Pre-place desired, .1, .2 with different content → expect .3 free.
	writeFile(t, filepath.Join(archDir, "src.mkv"), "XX", time.Unix(1, 0))
	writeFile(t, filepath.Join(archDir, "src.1.mkv"), "YY", time.Unix(2, 0))
	writeFile(t, filepath.Join(archDir, "src.2.mkv"), "ZZ", time.Unix(3, 0))

	got, already, err := resolveArchiveTarget(src, filepath.Join(archDir, "src.mkv"))
	if err != nil {
		t.Fatalf("resolveArchiveTarget: %v", err)
	}
	want := filepath.Join(archDir, "src.3.mkv")
	if got != want {
		t.Fatalf("got %s, want %s", got, want)
	}
	if already {
		t.Fatal("expected alreadyThere=false")
	}
}

func TestResolveArchiveTarget_SuffixChainFindsIdentical(t *testing.T) {
	// An identical copy already exists at .2 — should return it, alreadyThere=true.
	dir := t.TempDir()
	src := filepath.Join(dir, "src.mkv")
	mtime := time.Unix(1_700_000_000, 0)
	writeFile(t, src, "payload", mtime)
	archDir := filepath.Join(dir, "archive")
	writeFile(t, filepath.Join(archDir, "src.mkv"), "other", time.Unix(1, 0))
	writeFile(t, filepath.Join(archDir, "src.1.mkv"), "yet-another", time.Unix(2, 0))
	writeFile(t, filepath.Join(archDir, "src.2.mkv"), "payload", mtime)

	got, already, err := resolveArchiveTarget(src, filepath.Join(archDir, "src.mkv"))
	if err != nil {
		t.Fatalf("resolveArchiveTarget: %v", err)
	}
	want := filepath.Join(archDir, "src.2.mkv")
	if got != want {
		t.Fatalf("got %s, want %s", got, want)
	}
	if !already {
		t.Fatal("expected alreadyThere=true")
	}
}

// ---------------- SafeArchiveWithSidecars ----------------

func TestSafeArchiveWithSidecars_MovesOriginal(t *testing.T) {
	dir := t.TempDir()
	libRoot := filepath.Join(dir, "library")
	archRoot := filepath.Join(dir, "archive")
	src := filepath.Join(libRoot, "show", "ep1.mkv")
	enc := filepath.Join(libRoot, "show", ".ep1.mkv.mediforge.tmp")
	archiveTarget := filepath.Join(archRoot, "tv", "show", "ep1.mkv")
	subTmp := filepath.Join(libRoot, "show", ".ep1.mkv.sub.0.srt.tmp")
	subFinal := filepath.Join(libRoot, "show", "ep1.default.srt")

	writeFile(t, src, "original", time.Unix(1_700_000_000, 0))
	writeFile(t, enc, "encoded", time.Now())
	writeFile(t, subTmp, "sub", time.Now())

	final, err := SafeArchiveWithSidecars(src, enc, archiveTarget, []Sidecar{{TmpPath: subTmp, FinalPath: subFinal}})
	if err != nil {
		t.Fatalf("SafeArchiveWithSidecars: %v", err)
	}
	if final != filepath.Join(libRoot, "show", "ep1.mp4") {
		t.Fatalf("final = %s", final)
	}
	if got := readFile(t, archiveTarget); got != "original" {
		t.Fatalf("archived content = %q, want original", got)
	}
	if got := readFile(t, final); got != "encoded" {
		t.Fatalf("encoded content = %q", got)
	}
	if got := readFile(t, subFinal); got != "sub" {
		t.Fatalf("sub content = %q", got)
	}
	mustNotExist(t, src)
	mustNotExist(t, enc)
	mustNotExist(t, subTmp)
}

func TestSafeArchiveWithSidecars_IdenticalAlreadyArchived(t *testing.T) {
	// Existing identical archive copy → src is deleted, archive untouched.
	dir := t.TempDir()
	libRoot := filepath.Join(dir, "lib")
	archRoot := filepath.Join(dir, "arc")
	src := filepath.Join(libRoot, "ep.mkv")
	enc := filepath.Join(libRoot, ".ep.mkv.mediforge.tmp")
	archiveTarget := filepath.Join(archRoot, "tv", "ep.mkv")
	mtime := time.Unix(1_700_000_000, 0)

	writeFile(t, src, "identical", mtime)
	writeFile(t, archiveTarget, "identical", mtime)
	writeFile(t, enc, "encoded", time.Now())

	if _, err := SafeArchiveWithSidecars(src, enc, archiveTarget, nil); err != nil {
		t.Fatalf("SafeArchiveWithSidecars: %v", err)
	}
	if got := readFile(t, archiveTarget); got != "identical" {
		t.Fatalf("archive content = %q, want unchanged", got)
	}
	if got := readFile(t, filepath.Join(libRoot, "ep.mp4")); got != "encoded" {
		t.Fatalf("final content = %q", got)
	}
	mustNotExist(t, src)
	mustNotExist(t, enc)
}

func TestSafeArchiveWithSidecars_DifferentCollisionSuffixes(t *testing.T) {
	// Existing *different* archive copy → src lands at .1 suffix, both retained.
	dir := t.TempDir()
	libRoot := filepath.Join(dir, "lib")
	archRoot := filepath.Join(dir, "arc")
	src := filepath.Join(libRoot, "ep.mkv")
	enc := filepath.Join(libRoot, ".ep.mkv.mediforge.tmp")
	archiveTarget := filepath.Join(archRoot, "tv", "ep.mkv")

	writeFile(t, archiveTarget, "OLD-archived-copy", time.Unix(1, 0))
	writeFile(t, src, "NEW-source", time.Unix(2, 0))
	writeFile(t, enc, "encoded", time.Now())

	if _, err := SafeArchiveWithSidecars(src, enc, archiveTarget, nil); err != nil {
		t.Fatalf("SafeArchiveWithSidecars: %v", err)
	}
	if got := readFile(t, archiveTarget); got != "OLD-archived-copy" {
		t.Fatalf("pre-existing archive altered: %q", got)
	}
	suffixed := filepath.Join(archRoot, "tv", "ep.1.mkv")
	if got := readFile(t, suffixed); got != "NEW-source" {
		t.Fatalf("suffixed archive content = %q", got)
	}
	if got := readFile(t, filepath.Join(libRoot, "ep.mp4")); got != "encoded" {
		t.Fatalf("final content = %q", got)
	}
	mustNotExist(t, src)
}

// ---------------- moveFile ----------------

func TestMoveFile_SameDir(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.bin")
	b := filepath.Join(dir, "sub", "b.bin")
	writeFile(t, a, "payload", time.Unix(42, 0))
	if err := os.MkdirAll(filepath.Dir(b), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := moveFile(a, b); err != nil {
		t.Fatalf("moveFile: %v", err)
	}
	if got := readFile(t, b); got != "payload" {
		t.Fatalf("dst content = %q", got)
	}
	mustNotExist(t, a)
}

func TestCopyAndUnlink_PreservesMtimeAndRemovesSrc(t *testing.T) {
	// Exercise the cross-FS fallback directly. Even on same FS, copyAndUnlink
	// should work — it's the fallback path, not conditional on EXDEV itself.
	dir := t.TempDir()
	a := filepath.Join(dir, "a.bin")
	b := filepath.Join(dir, "b.bin")
	mtime := time.Unix(1_600_000_000, 0)
	writeFile(t, a, "content", mtime)

	if err := copyAndUnlink(a, b); err != nil {
		t.Fatalf("copyAndUnlink: %v", err)
	}
	if got := readFile(t, b); got != "content" {
		t.Fatalf("dst content = %q", got)
	}
	mustNotExist(t, a)
	st, err := os.Stat(b)
	if err != nil {
		t.Fatal(err)
	}
	if st.ModTime().Unix() != mtime.Unix() {
		t.Fatalf("mtime preserved? got %d, want %d", st.ModTime().Unix(), mtime.Unix())
	}
}
