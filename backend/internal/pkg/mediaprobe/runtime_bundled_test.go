//go:build bundled_ffprobe

package mediaprobe

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// The release gate exercises the same self-provisioning used after a legacy
// online updater replaces only the server binary. No system ffprobe is needed.
func TestBundledRuntimeWorksWithoutSystemFFprobe(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	_, err := exec.LookPath("ffprobe")
	require.Error(t, err)
	path, err := Ensure(context.Background())
	require.NoError(t, err)
	require.True(t, matchesDigest(path, bundledSHA256))
	video, err := filepath.Abs("testdata/reference.mp4")
	require.NoError(t, err)
	output, err := exec.Command(path, "-v", "error", "-show_entries",
		"format=duration:stream=codec_type,codec_name,width,height,r_frame_rate", "-of", "json", video).Output()
	require.NoError(t, err)
	var probe struct {
		Streams []struct {
			Codec string `json:"codec_name"`
			Width int    `json:"width"`
			FPS   string `json:"r_frame_rate"`
		} `json:"streams"`
		Format struct {
			Duration string `json:"duration"`
		} `json:"format"`
	}
	require.NoError(t, json.Unmarshal(output, &probe))
	require.Len(t, probe.Streams, 1)
	require.Equal(t, "h264", probe.Streams[0].Codec)
	require.Equal(t, 32, probe.Streams[0].Width)
	require.Equal(t, "24/1", probe.Streams[0].FPS)
	require.Equal(t, "1.000000", probe.Format.Duration)

	// Audio references use the same runtime. Generate one second of PCM WAV.
	wav := make([]byte, 44+16000)
	copy(wav, "RIFF")
	binary.LittleEndian.PutUint32(wav[4:], uint32(len(wav)-8))
	copy(wav[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(wav[16:], 16)
	binary.LittleEndian.PutUint16(wav[20:], 1)
	binary.LittleEndian.PutUint16(wav[22:], 1)
	binary.LittleEndian.PutUint32(wav[24:], 8000)
	binary.LittleEndian.PutUint32(wav[28:], 16000)
	binary.LittleEndian.PutUint16(wav[32:], 2)
	binary.LittleEndian.PutUint16(wav[34:], 16)
	copy(wav[36:], "data")
	binary.LittleEndian.PutUint32(wav[40:], 16000)
	audio := filepath.Join(t.TempDir(), "reference.wav")
	require.NoError(t, os.WriteFile(audio, wav, 0600))
	output, err = exec.Command(path, "-v", "error", "-show_entries", "format=duration", "-of", "json", audio).Output()
	require.NoError(t, err)
	require.Contains(t, string(output), `"duration": "1.000000"`)

	// Re-running preparation is idempotent; deleting the runtime repairs itself.
	again, err := Ensure(context.Background())
	require.NoError(t, err)
	require.Equal(t, path, again)
	require.NoError(t, os.WriteFile(path, []byte("corrupt"), 0700))
	again, err = Ensure(context.Background())
	require.NoError(t, err)
	require.Equal(t, path, again)
	require.True(t, matchesDigest(path, bundledSHA256))
	require.NoError(t, os.Remove(path))
	again, err = Ensure(context.Background())
	require.NoError(t, err)
	require.Equal(t, path, again)
}
