package handler

import (
	"bytes"
	"encoding/binary"
	"io"
	"mime/multipart"
	"net/http/httptest"
	"net/textproto"
	"os"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func referencePCM(t *testing.T, streaming bool) []byte {
	t.Helper()
	var data bytes.Buffer
	_, err := data.WriteString("RIFF")
	require.NoError(t, err)
	size := uint32(36 + 160)
	if streaming {
		size = ^uint32(0)
	}
	require.NoError(t, binary.Write(&data, binary.LittleEndian, size))
	_, err = data.WriteString("WAVEfmt ")
	require.NoError(t, err)
	for _, value := range []any{uint32(16), uint16(1), uint16(1), uint32(8000), uint32(16000), uint16(2), uint16(16)} {
		require.NoError(t, binary.Write(&data, binary.LittleEndian, value))
	}
	_, err = data.WriteString("data")
	require.NoError(t, err)
	size = 160
	if streaming {
		size = ^uint32(0)
	}
	require.NoError(t, binary.Write(&data, binary.LittleEndian, size))
	_, err = data.Write(make([]byte, 160))
	require.NoError(t, err)
	return data.Bytes()
}

func TestWAVReferenceMIMENormalization(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		data := referencePCM(t, streaming)
		require.Equal(t, "audio/wav", detectMediaMIME(data))
		for _, declared := range []string{"audio/wav", "audio/x-wav", "audio/wave", "audio/WAVE; codec=pcm"} {
			file, err := os.CreateTemp(t.TempDir(), "voice-")
			require.NoError(t, err)
			t.Cleanup(func() { _ = file.Close() })
			_, err = file.Write(data)
			require.NoError(t, err)
			_, err = file.Seek(0, io.SeekStart)
			require.NoError(t, err)
			policy, ct, ok := inspectMedia(file, &multipart.FileHeader{
				Filename: "voice.wav", Header: textproto.MIMEHeader{"Content-Type": {declared}}, Size: int64(len(data)),
			})
			require.True(t, ok, "valid WAV declared as %s", declared)
			require.Equal(t, "audio", policy.kind)
			require.Equal(t, "audio/wav", ct)
		}
	}
}

func TestWAVReferenceRejectsMismatchedMedia(t *testing.T) {
	for _, item := range []struct {
		filename, ct string
		data         []byte
	}{
		{"voice.mp3", "audio/mpeg", referencePCM(t, false)},
		{"voice.wav", "audio/wave", []byte("<html>not audio</html>")},
		{"voice.mp3", "audio/wav", referencePCM(t, false)},
	} {
		file, err := os.CreateTemp(t.TempDir(), "media-")
		require.NoError(t, err)
		t.Cleanup(func() { _ = file.Close() })
		_, err = file.Write(item.data)
		require.NoError(t, err)
		_, err = file.Seek(0, io.SeekStart)
		require.NoError(t, err)
		_, _, ok := inspectMedia(file, &multipart.FileHeader{Filename: item.filename, Header: textproto.MIMEHeader{"Content-Type": {item.ct}}})
		require.False(t, ok)
	}
}

func TestReceiveWAVReferenceAcceptsMIMEAliases(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, ct := range []string{"audio/wav", "audio/x-wav", "audio/wave"} {
		t.Run(ct, func(t *testing.T) {
			h, _ := newAgentHandlerMock(t)
			req, _ := multipartRequest(t, "voice.wav", ct, referencePCM(t, true))
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = req
			file, _, _, err := h.receiveTemporaryAsset(c)
			require.NoError(t, err)
			t.Cleanup(func() { _ = file.Close(); _ = os.Remove(file.Name()) })
			data, err := io.ReadAll(file)
			require.NoError(t, err)
			require.Equal(t, referencePCM(t, true), data)
		})
	}
}
