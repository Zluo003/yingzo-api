package service

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/png"
	"testing"

	"github.com/stretchr/testify/require"
)

func selectionTestPNG(t *testing.T, width, height int) string {
	t.Helper()
	var data bytes.Buffer
	require.NoError(t, png.Encode(&data, image.NewRGBA(image.Rect(0, 0, width, height))))
	return base64.StdEncoding.EncodeToString(data.Bytes())
}

func TestSingleImageSelectionUsesActualPixels(t *testing.T) {
	small := selectionTestPNG(t, 16, 8)
	large := selectionTestPNG(t, 64, 32)
	// Ignore declared dimensions, position and encoded length. Invalid image
	// content is not a deliverable, while transport errors fail publication.
	for _, tc := range []struct {
		name    string
		encoded []string
		want    int
		fail    bool
	}{
		{"upscale-last", []string{small, large}, 1, false},
		{"upscale-first", []string{large, small}, 0, false},
		{"direct-high", []string{large}, 0, false},
		{"equal-size", []string{large, large}, 0, false},
		{"corrupt-content", []string{base64.StdEncoding.EncodeToString([]byte("not an image")), large}, 1, false},
		{"all-corrupt", []string{base64.StdEncoding.EncodeToString([]byte("not an image"))}, 0, true},
		{"invalid-encoding", []string{small, "invalid base64"}, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var items []map[string]json.RawMessage
			for _, encoded := range tc.encoded {
				raw, _ := json.Marshal(encoded)
				items = append(items, map[string]json.RawMessage{"b64_json": raw, "width": json.RawMessage(`99999`)})
			}
			index, err := (&DurableImageService{}).selectLargestImage(context.Background(), items, &APIKey{}, "", NewImageResultUploader(nil, "", maxPublishedGeneratedImageBytes, nil))
			if tc.fail {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.want, index)
		})
	}
}
