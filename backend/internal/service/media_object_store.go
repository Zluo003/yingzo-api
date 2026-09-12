package service

import (
	"context"
	"io"
)

// MediaObjectStore avoids full-body buffering for known-size reference files.
// BackupObjectStore remains compatible with existing backup implementations.
type MediaObjectStore interface {
	UploadSized(context.Context, string, io.ReadSeeker, string, int64) (int64, error)
	Stat(context.Context, string) (int64, error)
	DownloadRange(context.Context, string, int64, int64) (io.ReadCloser, error)
}
