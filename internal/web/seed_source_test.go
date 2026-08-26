package web_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestSeedSourceHasNoDecoy pins which repo directory is the seed shipped to
// the Fly volume.
//
// Background (2026-08-20): the repo carried a top-level default-content/ that
// looked authoritative and was not. Dockerfile line 16 reads
//
//	COPY content/ /app/default-content/
//
// so default-content/ is the path INSIDE the image, and content/ is the repo
// source. entrypoint.sh then cp -f's /app/default-content/* onto the volume at
// every boot. A repo directory sharing the in-image name is therefore a decoy:
// edits land in it, look committed, and never reach production.
//
// Two skills died that way — local-stack-launch (41 lines) and
// persona-creation (89 lines) sat in default-content/skills/ from 2026-06-08,
// were in git, and returned "nie znaleziony" from the live server for two and a
// half months. Deleted 2026-08-20 on kapoost's call ("dorobimy nowe").
//
// This test asserts the COPY source exists and that no directory named after
// the in-image destination reappears at the repo root.
func TestSeedSourceHasNoDecoy(t *testing.T) {
	repoRoot, err := findRepoRoot()
	if err != nil {
		t.Fatalf("locate repo root: %v", err)
	}

	dockerfile, err := os.ReadFile(filepath.Join(repoRoot, "Dockerfile"))
	if err != nil {
		t.Fatalf("read Dockerfile: %v", err)
	}

	// COPY <src>/ /app/<dest>/ — the line entrypoint.sh later reads from.
	re := regexp.MustCompile(`(?m)^COPY\s+(\S+?)/?\s+/app/(\S+?)/?\s*$`)
	var src, dest string
	for _, m := range re.FindAllStringSubmatch(string(dockerfile), -1) {
		if strings.Contains(m[2], "content") {
			src, dest = m[1], m[2]
			break
		}
	}
	if src == "" {
		t.Fatal("Dockerfile has no COPY <src> /app/<...content...> line — " +
			"entrypoint.sh seeds the volume from that path; if the build " +
			"changed, update this test deliberately rather than deleting it")
	}

	if _, err := os.Stat(filepath.Join(repoRoot, src)); err != nil {
		t.Errorf("Dockerfile seeds the volume from %q, which does not exist in the repo: %v", src, err)
	}

	// The decoy check. dest is a path inside the image; a repo directory with
	// that name is the exact trap that swallowed two skills.
	if dest != src {
		if _, err := os.Stat(filepath.Join(repoRoot, dest)); err == nil {
			t.Errorf(
				"repo has a top-level %q directory, but the seed shipped to production is %q "+
					"(Dockerfile: COPY %s/ /app/%s/).\n"+
					"Anything placed in %q never reaches the Fly volume — it only looks like it does.\n"+
					"Fix by moving its contents into %q and deleting the decoy.",
				dest, src, src, dest, dest, src)
		}
	}
}
