//go:build !bundled_ffprobe

package mediaprobe

// Development images install ffprobe through their package manager. Release
// builds must generate a platform payload; missing payloads fail compilation.
var bundledGZIP []byte

const bundledSHA256 = ""
