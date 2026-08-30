package storage

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/traP-jp/kinugasa-recording/internal/shared/workerprotocol"
)

func ResolvePath(sharedVolume, relativePath string) (string, error) {
	if err := workerprotocol.ValidateRelativePath("relative_path", relativePath); err != nil {
		return "", err
	}
	root, err := filepath.Abs(sharedVolume)
	if err != nil {
		return "", fmt.Errorf("resolve shared volume: %w", err)
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return "", fmt.Errorf("resolve shared volume symlinks: %w", err)
	}
	current := root
	components := strings.Split(relativePath, "/")
	for index, component := range components {
		next := filepath.Join(current, component)
		info, err := os.Lstat(next)
		if errors.Is(err, os.ErrNotExist) {
			return filepath.Join(append([]string{current}, components[index:]...)...), nil
		}
		if err != nil {
			return "", fmt.Errorf("inspect path component %q: %w", component, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			next, err = filepath.EvalSymlinks(next)
			if err != nil {
				return "", fmt.Errorf("resolve path component %q: %w", component, err)
			}
			if !isWithin(root, next) {
				return "", fmt.Errorf("relative path resolves outside shared volume")
			}
		}
		current = next
	}
	if !isWithin(root, current) {
		return "", fmt.Errorf("relative path resolves outside shared volume")
	}
	return current, nil
}

func isWithin(root, candidate string) bool {
	relative, err := filepath.Rel(root, candidate)
	if err != nil {
		return false
	}
	return relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}
