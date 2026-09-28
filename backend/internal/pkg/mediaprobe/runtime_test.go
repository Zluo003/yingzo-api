package mediaprobe

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

func testPayload(t *testing.T, raw []byte) ([]byte, string) {
	t.Helper()
	var compressed bytes.Buffer
	w := gzip.NewWriter(&compressed)
	_, err := w.Write(raw)
	require.NoError(t, err)
	require.NoError(t, w.Close())
	return compressed.Bytes(), fmt.Sprintf("%x", sha256.Sum256(raw))
}

func TestInstallRejectsDamagedPayload(t *testing.T) {
	root := t.TempDir()
	payload, digest := testPayload(t, []byte("binary"))
	payload[len(payload)-1] ^= 0xff
	_, err := install(context.Background(), root, payload, digest)
	require.Error(t, err)
	files, err := filepath.Glob(filepath.Join(root, "*", "*"))
	require.NoError(t, err)
	require.Empty(t, files)
}

func TestInstallRejectsChecksumMismatch(t *testing.T) {
	payload, _ := testPayload(t, []byte("binary"))
	_, err := install(context.Background(), t.TempDir(), payload, strings.Repeat("0", 64))
	require.ErrorContains(t, err, "checksum mismatch")
}

func TestInstallRepairsCorruptRuntimeAndSupportsConcurrency(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture; Windows executable is covered by the bundled runtime test")
	}
	payload, digest := testPayload(t, []byte("#!/bin/sh\nexit 0\n"))
	root := t.TempDir()
	path, err := install(context.Background(), root, payload, digest)
	require.NoError(t, err)
	require.True(t, matchesDigest(path, digest))
	require.NoError(t, os.WriteFile(path, []byte("corrupt"), 0700))
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < cap(errs); i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := install(context.Background(), root, payload, digest)
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	require.True(t, matchesDigest(path, digest))
}

func TestInstallReportsUnwritableDestination(t *testing.T) {
	root := filepath.Join(t.TempDir(), "file")
	require.NoError(t, os.WriteFile(root, []byte("not a directory"), 0600))
	payload, digest := testPayload(t, []byte("binary"))
	_, err := install(context.Background(), root, payload, digest)
	require.ErrorContains(t, err, "runtime directory")
}
