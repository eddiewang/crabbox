package runnerfs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWindowsRootedAndDriveRelativePaths(t *testing.T) {
	base := t.TempDir()
	working := filepath.Join(base, "working")
	target := filepath.Join(base, "target")
	if err := os.Mkdir(working, 0o700); err != nil {
		t.Fatal(err)
	}
	writeFixture(t, filepath.Join(target, "junit.xml"), "report")
	t.Chdir(working)
	volume := filepath.VolumeName(working)
	if len(volume) != 2 || volume[1] != ':' {
		t.Skip("fixture requires a local Windows drive")
	}
	for _, name := range []string{strings.TrimPrefix(target, volume), volume + `..\target`} {
		root, err := OpenRoot(name)
		if err != nil {
			t.Fatal(err)
		}
		file, readErr := root.Read("junit.xml", 64)
		_ = root.Close()
		if readErr != nil || string(file.Data) != "report" {
			t.Fatalf("root %q: data=%q err=%v", name, file.Data, readErr)
		}
		destination, err := ArchiveTarget(name+`\destination`, "payload", false)
		if err != nil {
			t.Fatal(err)
		}
		assertWindowsArchiveParent(t, destination, target)
	}
}

func TestWindowsDriveRelativePathPreservesPhysicalParent(t *testing.T) {
	root, dir := physicalParentFixture(t)
	_ = root.Close()
	t.Chdir(dir)
	volume := filepath.VolumeName(dir)
	if len(volume) != 2 || volume[1] != ':' {
		t.Skip("fixture requires a local Windows drive")
	}
	parent, err := OpenRoot(volume + `alias\..`)
	if err != nil {
		t.Fatal(err)
	}
	defer parent.Close()
	file, err := parent.Read("junit.xml", 64)
	if err != nil || string(file.Data) != "FAIL" {
		t.Fatalf("drive-relative physical parent: data=%q err=%v", file.Data, err)
	}
}

func TestWindowsRootRelativeSymlinkKeepsAbsoluteIdentity(t *testing.T) {
	base := t.TempDir()
	t.Chdir(base)
	target := filepath.Join(base, "target")
	writeFixture(t, filepath.Join(target, "junit.xml"), "report")
	volume := filepath.VolumeName(target)
	if len(volume) != 2 || volume[1] != ':' {
		t.Skip("fixture requires a local Windows drive")
	}
	alias := filepath.Join(base, "alias")
	symlinkFixture(t, strings.TrimPrefix(target, volume), alias)
	for _, name := range []string{alias, strings.TrimPrefix(alias, volume)} {
		root, err := OpenRoot(name)
		if err != nil {
			t.Fatal(err)
		}
		file, readErr := root.Read(filepath.Join(target, "junit.xml"), 64)
		_ = root.Close()
		if readErr != nil || string(file.Data) != "report" {
			t.Fatalf("root-relative link %q: data=%q err=%v", name, file.Data, readErr)
		}
		destination, err := ArchiveTarget(name+`\destination`, "payload", false)
		if err != nil {
			t.Fatal(err)
		}
		assertWindowsArchiveParent(t, destination, target)
	}
}

func assertWindowsArchiveParent(t *testing.T, destination, parent string) {
	t.Helper()
	if !filepath.IsAbs(destination) || filepath.Base(destination) != "destination" {
		t.Fatalf("destination is not an absolute selected leaf: %q", destination)
	}
	actual, err := os.Stat(filepath.Dir(destination))
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.Stat(parent)
	if err != nil || !os.SameFile(actual, want) {
		t.Fatalf("destination parent identity differs: %q versus %q, err=%v", destination, parent, err)
	}
}

func TestWindowsArchiveTrailingBackslashIntent(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, filepath.Join(dir, "file.txt"), "source")
	source, archive, err := CreateArchive(t.Context(), dir+`\`, CreateOptions{}, DefaultArchiveLimits())
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(archive.Name())
	defer archive.Close()
	if !source.ContentsOnly {
		t.Fatal("native trailing separator lost contents-only intent")
	}
	target, err := ArchiveTarget(filepath.Join(dir, "new-directory")+`\`, "destination", false)
	if err != nil {
		t.Fatal(err)
	}
	assertWindowsArchiveParent(t, target, filepath.Join(dir, "new-directory"))
	for _, name := range []string{dir + `\.`, dir + `\..`, filepath.Join(dir, "file.txt") + `\`} {
		_, invalidArchive, err := CreateArchive(t.Context(), name, CreateOptions{}, DefaultArchiveLimits())
		if invalidArchive != nil {
			invalidArchive.Close()
			os.Remove(invalidArchive.Name())
		}
		if err == nil {
			t.Fatalf("accepted invalid trailing component: %q", name)
		}
	}
}
