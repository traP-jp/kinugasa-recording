package storage

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolvePath(t *testing.T) {
	root := t.TempDir()
	resolved, err := ResolvePath(root, "recordings/take-1/video.mp4")
	if err != nil {
		t.Fatalf("ResolvePath() error = %v", err)
	}
	if want := filepath.Join(root, "recordings", "take-1", "video.mp4"); resolved != want {
		t.Fatalf("ResolvePath() = %q, want %q", resolved, want)
	}
}

func TestResolvePathAllowsInternalSymlink(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "recordings")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatalf("mkdir target: %v", err)
	}
	if err := os.Symlink(target, filepath.Join(root, "alias")); err != nil {
		t.Fatalf("create symlink: %v", err)
	}
	resolved, err := ResolvePath(root, "alias/video.mp4")
	if err != nil {
		t.Fatalf("ResolvePath() error = %v", err)
	}
	if want := filepath.Join(target, "video.mp4"); resolved != want {
		t.Fatalf("ResolvePath() = %q, want %q", resolved, want)
	}
}

func TestResolvePathRejectsEscapingSymlink(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "outside")); err != nil {
		t.Fatalf("create symlink: %v", err)
	}
	if _, err := ResolvePath(root, "outside/video.mp4"); err == nil {
		t.Fatal("ResolvePath() error = nil for escaping symlink")
	}
}
