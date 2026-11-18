package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSeedanceFlashDiscoveryAndChannelCapabilities(t *testing.T) {
	for _, tc := range []struct {
		provider string
		upstream string
		want     bool
	}{
		{videoProviderNewtokenOfficial, VideoModelSeedance25Flash, true},
		{videoProviderXingguang, VideoModelSeedance25Flash, true},
		{videoProviderJingyu, "custom-flash", true},
		{videoProviderJingyu, VideoModelSeedance25Flash, false},
	} {
		account := channelAccount(1, 0, "", "")
		account.Extra[VideoProviderExtraKey] = tc.provider
		account.Credentials["model_mapping"] = map[string]any{VideoModelSeedance25Flash: tc.upstream}
		discovered := discoverAgentModels([]Account{account})
		require.Equal(t, tc.want, len(discovered) == 1, tc.provider)
		prices := []AgentModelPrice{{Resolution: VideoResolution720P, BillingUnit: AgentBillingUnitSecond, UnitPrice: 0.1}}
		catalog, _ := channelCatalog([]Account{account}, prices)
		models := []AgentGroupModel{{ModelCode: VideoModelSeedance25Flash, MediaType: AgentMediaTypeVideo, Prices: prices}}
		require.NoError(t, catalog.attachVideoChannels(context.Background(), 20, models))
		channels := PublicVideoChannels(models[0].VideoChannels)
		require.Equal(t, tc.want, len(channels) == 1, tc.provider)
		if tc.want {
			require.Equal(t, VideoModelSeedance25Flash, discovered[0].ModelCode)
			require.Equal(t, []string{VideoResolution720P}, channels[0].Resolutions)
			require.Contains(t, channels[0].DurationsByResolution[VideoResolution720P], 30)
		}
	}
}

func TestSeedanceFlashUpstreamSelection(t *testing.T) {
	r := officialRequest(t, `{"model":"seedance-2.5-flash","prompt":"move","duration":30,"ratio":"16:9"}`)
	for _, tc := range []struct {
		provider string
		mapping  map[string]any
		want     string
	}{
		{videoProviderNewtokenOfficial, nil, "doubao-seedance-2-5-flash-261115"},
		{videoProviderXingguang, map[string]any{VideoModelSeedance25Flash: VideoModelSeedance25Flash}, "seedance2.5-flash-M"},
		{videoProviderJingyu, nil, ""},
		{videoProviderJingyu, map[string]any{VideoModelSeedance25Flash: VideoModelSeedance25Flash}, ""},
		{videoProviderJingyu, map[string]any{"seedance-*": "shared-id", VideoModelSeedance25: "standard-id"}, ""},
		{videoProviderJingyu, map[string]any{VideoModelSeedance25Flash: " custom-flash "}, "custom-flash"},
		{videoProviderXingguang, map[string]any{VideoModelSeedance25Flash: "custom-xing"}, "custom-xing"},
		{videoProviderNewtokenOfficial, map[string]any{VideoModelSeedance25Flash: "custom-official"}, "custom-official"},
	} {
		account := &Account{Extra: map[string]any{VideoProviderExtraKey: tc.provider}, Credentials: map[string]any{"model_mapping": tc.mapping}}
		require.Equal(t, tc.want != "", isVideoAccountCompatibleForRequest(account, r), tc.provider)
		id := videoUpstreamModelForAccount(account, r)
		require.Equal(t, tc.want, id, tc.provider)
		if id != "" {
			require.Equal(t, id, videoUpstreamBodyForAccount(account, r, id)["model"], tc.provider)
		}
		require.Equal(t, VideoModelSeedance25Flash, r.Model)
	}
}

func TestSeedance25Official4KRouting(t *testing.T) {
	r := officialRequest(t, `{"model":"seedance-2.5","prompt":"move","duration":5,"resolution":"4K"}`)
	for _, provider := range supportedVideoProviders {
		a := &Account{Extra: map[string]any{VideoProviderExtraKey: provider}}
		require.Equal(t, provider == videoProviderNewtokenOfficial, isVideoAccountCompatibleForRequest(a, r), provider)
		if provider == videoProviderNewtokenOfficial {
			require.Equal(t, "4k", videoUpstreamBodyForAccount(a, r, videoUpstreamModelForAccount(a, r))["resolution"])
		}
	}
}

func TestSeedanceFlashReferenceModesAndLimits(t *testing.T) {
	official := &Account{Extra: map[string]any{VideoProviderExtraKey: videoProviderNewtokenOfficial}}
	xing := &Account{Extra: map[string]any{VideoProviderExtraKey: videoProviderXingguang}}
	r := officialRequest(t, `{"model":"seedance-2.5-flash","prompt":"move","content":[{"type":"audio_url","audio_url":{"url":"https://cdn.example/ref.mp3"}}]}`)
	require.True(t, isVideoAccountCompatibleForRequest(official, r))
	require.False(t, isVideoAccountCompatibleForRequest(xing, r))
	for mode, duration := range map[string]int{"edit": -1, "extend": 30} {
		body, err := json.Marshal(map[string]any{"model": VideoModelSeedance25Flash, "prompt": "move", "ratio": "adaptive", "omni_reference_task_type": mode, "duration": duration, "content": []VideoContent{{Type: "video_url", VideoURL: &VideoContentURL{URL: "asset://ref"}}}})
		require.NoError(t, err)
		r := officialRequest(t, string(body))
		require.True(t, isVideoAccountCompatibleForRequest(official, r))
		out := videoUpstreamBodyForAccount(official, r, videoUpstreamModelForAccount(official, r))
		require.Equal(t, duration, out["duration"])
		require.Equal(t, mode, out["omni_reference_task_type"])
	}
	for _, field := range []string{`"resolution":"1080p"`, `"resolution":"4K"`, `"draft":true`, `"content":[{"type":"draft_task","draft_task":{"id":"task"}}]`} {
		var req VideoCreateRequest
		body := []byte(`{"model":"seedance-2.5-flash","prompt":"move",` + field + `}`)
		require.NoError(t, json.Unmarshal(body, &req))
		require.NoError(t, json.Unmarshal(body, &req.Raw))
		_, err := normalizeVideoCreateRequest(&req)
		require.Error(t, err, field)
	}
	for count := range 6 {
		r.Content = append(r.Content, r.Content[0])
		require.Equal(t, count < 4, isVideoAccountCompatibleForRequest(official, r))
	}
}
