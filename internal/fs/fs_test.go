package fs

import (
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// waitUntil condition returns true or timeout is reached.
func waitUntil(t *testing.T, d time.Duration, condition func() bool) {
	t.Helper()

	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}

		time.Sleep(10 * time.Millisecond)
	}

	t.Fatalf("Condition not met within %s", d)
}

// Direct write to file.
// Create file, start watching it, modify file, expect handler to be triggered.
func Test_WatchFile_DirectWrite(t *testing.T) {
	var hits int32

	onChange := func(_ string) {
		atomic.AddInt32(&hits, 1)
	}

	dir := t.TempDir()
	file := filepath.Join(dir, "token")

	// Create new file.
	require.NoError(t, os.WriteFile(file, []byte("initial content"), 0o640))

	// Start watching file.
	require.NoError(t, WatchFile(t.Context(), file, onChange))

	// Modify file.
	require.NoError(t, os.WriteFile(file, []byte("modified content"), 0o640))

	// Wait until change is detected and onChange handler triggered (hits >= 1).
	waitUntil(t, time.Second, func() bool { return atomic.LoadInt32(&hits) >= 1 })
}

// Symlink swap:
//
//	File:    dir/subdir1/file
//	File:    dir/subdir2/file
//	Symlink: dir/file -> Symlink to dir/subdir1/file (then swap to subdir2)
func Test_WatchFile_SymlinkSwap(t *testing.T) {
	var hits int32

	onChange := func(_ string) {
		atomic.AddInt32(&hits, 1)
	}

	dir := t.TempDir()
	file1 := filepath.Join(dir, "subdir1", "file")
	file2 := filepath.Join(dir, "subdir2", "file")
	require.NoError(t, os.MkdirAll(filepath.Dir(file1), 0o750))
	require.NoError(t, os.MkdirAll(filepath.Dir(file2), 0o750))
	require.NoError(t, os.WriteFile(file1, []byte("content"), 0o640))
	require.NoError(t, os.WriteFile(file2, []byte("content"), 0o640)) // Same content!

	// Create dir/file symlink to dir/subdir1/file
	symlink := filepath.Join(dir, "file")
	require.NoError(t, os.Symlink(file1, symlink))

	// Start watching dir/file for changes.
	require.NoError(t, WatchFile(t.Context(), symlink, onChange))

	// Atomic symlink swap (similar to how Kubelet does it).
	tmpLink := filepath.Join(dir, "file_tmp")
	require.NoError(t, os.Symlink(file2, tmpLink))
	require.NoError(t, os.Rename(tmpLink, symlink))

	// Wait until change is detected and onChange handler triggered (hits >= 1).
	waitUntil(t, time.Second, func() bool { return atomic.LoadInt32(&hits) >= 1 })
}
