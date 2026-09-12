package service

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

var ErrOpenAIImagePublication = errors.New("generated image URL publication failed")

func openAIImagePublicationClientMessage(err error) string {
	if errors.Is(err, ErrOpenAIImagePublication) {
		return "Generated image URL is temporarily unavailable"
	}
	if err == nil {
		return "Generated image URL is temporarily unavailable"
	}
	return err.Error()
}

type openAIImageURLPublicationContextKey struct{}

type openAIImageURLPublication struct {
	publisher             OpenAIImageResultPublisher
	owner                 TemporaryAssetOwner
	fallbackPublicBaseURL string
}

// WithOpenAIImageURLPublication enables real temporary URLs for a single
// public Agent request. The publisher and owner never enter response JSON.
func WithOpenAIImageURLPublication(
	ctx context.Context,
	publisher OpenAIImageResultPublisher,
	owner TemporaryAssetOwner,
	fallbackPublicBaseURL string,
) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if publisher == nil {
		return ctx
	}
	return context.WithValue(ctx, openAIImageURLPublicationContextKey{}, openAIImageURLPublication{
		publisher:             publisher,
		owner:                 owner,
		fallbackPublicBaseURL: strings.TrimSpace(fallbackPublicBaseURL),
	})
}

func openAIImageURLPublicationFromContext(ctx context.Context) (openAIImageURLPublication, bool) {
	if ctx == nil {
		return openAIImageURLPublication{}, false
	}
	publication, ok := ctx.Value(openAIImageURLPublicationContextKey{}).(openAIImageURLPublication)
	return publication, ok && publication.publisher != nil
}

func openAIImageURLPublicationEnabled(ctx context.Context, responseFormat string) bool {
	if !strings.EqualFold(strings.TrimSpace(responseFormat), "url") {
		return false
	}
	_, ok := openAIImageURLPublicationFromContext(ctx)
	return ok
}

func publishOpenAIImageResultURL(ctx context.Context, encodedImage, outputFormat string) (string, error) {
	publication, ok := openAIImageURLPublicationFromContext(ctx)
	if !ok {
		return "", errors.New("image URL publisher is unavailable")
	}
	resultURL, err := publication.publisher.PublishGeneratedImage(
		ctx,
		publication.owner,
		publication.fallbackPublicBaseURL,
		encodedImage,
		outputFormat,
	)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrOpenAIImagePublication, err)
	}
	parsed, err := url.Parse(strings.TrimSpace(resultURL))
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.User != nil {
		return "", fmt.Errorf("%w: publisher returned an invalid HTTP(S) URL", ErrOpenAIImagePublication)
	}
	return parsed.String(), nil
}

// transformOpenAIImagesURLResponse replaces b64_json or data URLs in a native
// Images response with managed HTTP(S) temporary assets. Existing HTTP(S) URLs
// are preserved. It is a no-op unless publication was enabled by the handler.
func transformOpenAIImagesURLResponse(ctx context.Context, body []byte) ([]byte, error) {
	if _, ok := openAIImageURLPublicationFromContext(ctx); !ok {
		return body, nil
	}
	if len(body) == 0 || !gjson.ValidBytes(body) {
		return nil, errors.New("image URL response is not valid JSON")
	}

	transformed := append([]byte(nil), body...)
	rootFormat := strings.TrimSpace(gjson.GetBytes(transformed, "output_format").String())
	if data := gjson.GetBytes(transformed, "data"); data.IsArray() {
		for index, item := range data.Array() {
			path := fmt.Sprintf("data.%d", index)
			itemFormat := strings.TrimSpace(item.Get("output_format").String())
			if itemFormat == "" {
				itemFormat = rootFormat
			}
			encoded, existingHTTPURL, err := imagePayloadForPublication(item.Get("b64_json").String(), item.Get("url").String())
			if err != nil {
				return nil, fmt.Errorf("transform image result %d: %w", index, err)
			}
			if encoded != "" {
				publishedURL, err := publishOpenAIImageResultURL(ctx, encoded, itemFormat)
				if err != nil {
					return nil, fmt.Errorf("publish image result %d: %w", index, err)
				}
				transformed, _ = sjson.SetBytes(transformed, path+".url", publishedURL)
			} else if existingHTTPURL == "" && item.Get("b64_json").Exists() {
				return nil, fmt.Errorf("image result %d has an empty base64 payload", index)
			}
			transformed, _ = sjson.DeleteBytes(transformed, path+".b64_json")
		}
		return transformed, nil
	}

	encoded, existingHTTPURL, err := imagePayloadForPublication(
		gjson.GetBytes(transformed, "b64_json").String(),
		gjson.GetBytes(transformed, "url").String(),
	)
	if err != nil {
		return nil, err
	}
	if encoded != "" {
		publishedURL, err := publishOpenAIImageResultURL(ctx, encoded, rootFormat)
		if err != nil {
			return nil, fmt.Errorf("publish image result: %w", err)
		}
		transformed, _ = sjson.SetBytes(transformed, "url", publishedURL)
	} else if existingHTTPURL == "" && gjson.GetBytes(transformed, "b64_json").Exists() {
		return nil, errors.New("image result has an empty base64 payload")
	}
	transformed, _ = sjson.DeleteBytes(transformed, "b64_json")
	return transformed, nil
}

func imagePayloadForPublication(b64JSON, rawURL string) (encoded string, existingHTTPURL string, err error) {
	if encoded = strings.TrimSpace(b64JSON); encoded != "" {
		return encoded, "", nil
	}
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return "", "", nil
	}
	parsed, parseErr := url.Parse(rawURL)
	if parseErr == nil && parsed.Host != "" && (parsed.Scheme == "http" || parsed.Scheme == "https") && parsed.User == nil {
		return "", parsed.String(), nil
	}
	if !strings.HasPrefix(strings.ToLower(rawURL), "data:image/") {
		return "", "", errors.New("image result URL is not HTTP(S)")
	}
	comma := strings.IndexByte(rawURL, ',')
	if comma <= 0 || !strings.HasSuffix(strings.ToLower(rawURL[:comma]), ";base64") {
		return "", "", errors.New("image data URL is not base64 encoded")
	}
	encoded = strings.TrimSpace(rawURL[comma+1:])
	if encoded == "" {
		return "", "", errors.New("image data URL is empty")
	}
	if _, decodeErr := base64.StdEncoding.DecodeString(encoded); decodeErr != nil {
		if _, rawDecodeErr := base64.RawStdEncoding.DecodeString(encoded); rawDecodeErr != nil {
			return "", "", errors.New("image data URL contains invalid base64")
		}
	}
	return encoded, "", nil
}
