package service

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"image"
	"strings"

	"github.com/google/uuid"
)

// Keep the original ordinal for deterministic publication/recovery. Ties keep
// the first valid image; neither response order nor file size determines quality.
func (s *DurableImageService) selectLargestImage(ctx context.Context, items []map[string]json.RawMessage, key *APIKey, base string, fetcher *ImageResultUploader) (int, error) {
	best, largest := -1, int64(0)
	var bestBytes []byte
	for i, item := range items {
		var rawURL string
		_ = json.Unmarshal(item["url"], &rawURL)
		owner := ImageTaskOwner{UserID: key.UserID, APIKeyID: key.ID}
		if ref, ok := ParseTemporaryAssetRef(rawURL, "", ""); ok && ref.ID != uuid.Nil {
			if _, err := s.assetURL(ctx, ref.ID.String(), owner, base); err != nil {
				return 0, err
			}
			area, err := s.ownedImageArea(ctx, ref.ID.String(), owner)
			if err != nil {
				return 0, err
			}
			if area > largest {
				best, largest, bestBytes = i, area, nil
			}
			continue
		}
		if rawURL != "" && !strings.HasPrefix(rawURL, "data:") {
			if err := validateGeneratedVideoURL(ctx, rawURL, false); err != nil {
				return 0, err
			}
		}
		data, _, err := fetcher.fetchImageBytes(ctx, item)
		if err != nil {
			// An unavailable candidate may be the final upscale. Do not silently
			// deliver a draft and charge success when comparison cannot complete.
			return 0, err
		}
		if int64(len(data)) > maxPublishedGeneratedImageBytes {
			return 0, errors.New("generated image exceeds size limit")
		}
		cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
		if err != nil || cfg.Width <= 0 || cfg.Height <= 0 {
			continue
		}
		area := int64(cfg.Width) * int64(cfg.Height)
		if area > largest {
			best, largest, bestBytes = i, area, data
		}
	}
	if best < 0 {
		return 0, errors.New("upstream returned no valid images")
	}
	if bestBytes != nil {
		// Reuse downloaded bytes instead of fetching a mutable URL twice.
		items[best]["b64_json"], _ = json.Marshal(base64.StdEncoding.EncodeToString(bestBytes))
		delete(items[best], "url")
	}
	return best, nil
}

func (s *DurableImageService) ownedImageArea(ctx context.Context, id string, owner ImageTaskOwner) (int64, error) {
	var metadata []byte
	err := s.files.db.QueryRowContext(ctx, `SELECT metadata FROM temporary_assets WHERE id=$1 AND user_id=$2 AND api_key_id=$3 AND purpose='generated' AND media_type='image'`, id, owner.UserID, owner.APIKeyID).Scan(&metadata)
	if err != nil {
		return 0, err
	}
	var dimensions struct{ Width, Height int64 }
	if err := json.Unmarshal(metadata, &dimensions); err != nil {
		return 0, err
	}
	if dimensions.Width <= 0 || dimensions.Height <= 0 {
		return 0, errors.New("generated image dimensions unavailable")
	}
	return dimensions.Width * dimensions.Height, nil
}
