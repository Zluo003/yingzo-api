// Package mediaprobe provisions the ffprobe executable shipped inside release
// binaries. Keeping the payload in the server also supports older online
// updaters, which extract only the server executable from release archives.
package mediaprobe

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"time"
)

var ErrUnavailable = errors.New("trusted media probe (ffprobe) is unavailable")

var managed struct {
	sync.Mutex
	path string
	info os.FileInfo
}

// Ensure installs and verifies the bundled probe without root or network access.
// Source builds without the bundled_ffprobe tag use the system installation.
func Ensure(ctx context.Context) (string, error) {
	managed.Lock()
	defer managed.Unlock()
	if managed.path != "" {
		if info, err := os.Lstat(managed.path); err == nil && info.Mode().IsRegular() && managed.info != nil &&
			os.SameFile(info, managed.info) && info.Size() == managed.info.Size() && info.ModTime().Equal(managed.info.ModTime()) {
			return managed.path, nil
		}
		managed.path = ""
	}
	if len(bundledGZIP) == 0 {
		path, err := exec.LookPath("ffprobe")
		if err != nil {
			return "", fmt.Errorf("%w: install ffmpeg or use a release with the bundled runtime", ErrUnavailable)
		}
		return path, nil
	}
	executable, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("%w: locate server: %v", ErrUnavailable, err)
	}
	if resolved, err := filepath.EvalSymlinks(executable); err == nil {
		executable = resolved
	}
	roots := []string{filepath.Join(filepath.Dir(executable), ".runtime")}
	if cache, err := os.UserCacheDir(); err == nil {
		roots = append(roots, filepath.Join(cache, "yingzo-api", "runtime"))
	}
	var failures []error
	for _, root := range roots {
		path, err := install(ctx, root, bundledGZIP, bundledSHA256)
		if err == nil {
			managed.path = path
			managed.info, _ = os.Lstat(path)
			return path, nil
		}
		failures = append(failures, err)
		if ctx.Err() != nil {
			break
		}
	}
	return "", fmt.Errorf("%w: %w", ErrUnavailable, errors.Join(failures...))
}

func install(ctx context.Context, root string, payload []byte, digest string) (string, error) {
	decoded, err := hex.DecodeString(digest)
	if err != nil || len(decoded) != sha256.Size {
		return "", errors.New("invalid bundled ffprobe checksum")
	}
	dir := filepath.Join(root, "ffprobe-"+digest)
	name := "ffprobe"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	path := filepath.Join(dir, name)
	if matchesDigest(path, digest) {
		if err := checkExecutable(ctx, path); err == nil {
			return path, nil
		}
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", fmt.Errorf("create ffprobe runtime directory: %w", err)
	}
	in, err := gzip.NewReader(bytes.NewReader(payload))
	if err != nil {
		return "", fmt.Errorf("open bundled ffprobe: %w", err)
	}
	defer in.Close()
	out, err := os.CreateTemp(dir, ".ffprobe-*")
	if err != nil {
		return "", err
	}
	tmp := out.Name()
	defer os.Remove(tmp)
	defer out.Close()
	hash := sha256.New()
	const maxProbeSize = 128 << 20
	n, err := io.Copy(io.MultiWriter(out, hash), io.LimitReader(in, maxProbeSize+1))
	if err != nil {
		return "", fmt.Errorf("extract bundled ffprobe: %w", err)
	}
	if n > maxProbeSize || hex.EncodeToString(hash.Sum(nil)) != digest {
		return "", errors.New("bundled ffprobe checksum mismatch")
	}
	if err := out.Chmod(0700); err != nil {
		return "", err
	}
	if err := out.Sync(); err != nil {
		return "", err
	}
	if err := out.Close(); err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, path); err != nil {
		// Another process may have installed this exact version concurrently.
		if !matchesDigest(path, digest) {
			return "", fmt.Errorf("publish bundled ffprobe: %w", err)
		}
	}
	if err := checkExecutable(ctx, path); err != nil {
		return "", err
	}
	return path, nil
}

func matchesDigest(path, digest string) bool {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return false
	}
	file, err := os.Open(path)
	if err != nil {
		return false
	}
	defer file.Close()
	hash := sha256.New()
	_, err = io.Copy(hash, file)
	return err == nil && hex.EncodeToString(hash.Sum(nil)) == digest
}

func checkExecutable(ctx context.Context, path string) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := exec.CommandContext(ctx, path, "-version").Run(); err != nil {
		return fmt.Errorf("execute bundled ffprobe: %w", err)
	}
	return nil
}
