package youtube

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

var (
	ErrNotLive      = errors.New("channel or video is not currently live")
	ErrNotFound     = errors.New("target not found")
)

type InnerTubeClient struct {
	client *http.Client
}

func NewClient() *InnerTubeClient {
	return &InnerTubeClient{
		client: &http.Client{Timeout: 15 * time.Second},
	}
}

// buildLiveURL constructs the YouTube live page URL for a given target.
// It also handles full YouTube URLs passed as targets, extracting the
// video ID or handle before constructing the resolve URL.
func buildLiveURL(target string) string {
	// Defense-in-depth: if target is a full URL, extract the meaningful part
	if strings.HasPrefix(target, "http://") || strings.HasPrefix(target, "https://") {
		if u, err := url.Parse(target); err == nil {
			host := u.Hostname()
			if host == "youtube.com" || host == "www.youtube.com" || host == "youtu.be" || strings.HasSuffix(host, ".youtube.com") {
				// /watch?v=VIDEO_ID
				if v := u.Query().Get("v"); v != "" {
					target = v
				} else if host == "youtu.be" {
					// youtu.be/VIDEO_ID
					target = strings.TrimPrefix(u.Path, "/")
				} else if strings.HasPrefix(u.Path, "/live/") {
					// /live/VIDEO_ID
					parts := strings.Split(u.Path, "/")
					if len(parts) >= 3 && parts[2] != "" {
						target = parts[2]
					}
				} else if strings.HasPrefix(u.Path, "/@") {
					// /@handle or /@handle/live
					parts := strings.Split(u.Path, "/")
					if len(parts) >= 2 {
						target = parts[1] // @handle
					}
				}
			}
		}
	}

	if strings.HasPrefix(target, "@") || strings.HasPrefix(target, "UC") {
		return "https://www.youtube.com/" + target + "/live"
	}
	isLowercaseHandle := len(target) == 11 && regexp.MustCompile(`^[a-z]+$`).MatchString(target)
	if len(target) != 11 || isLowercaseHandle {
		return "https://www.youtube.com/@" + target + "/live"
	}
	return "https://www.youtube.com/watch?v=" + target
}

const clientVersion = "2.20260520.01.00"

func (c *InnerTubeClient) innertubeContext() map[string]any {
	return map[string]any{
		"client": map[string]string{
			"clientName":    "WEB",
			"clientVersion": clientVersion,
		},
	}
}

// setStandardHeaders adds browser-like headers required by YouTube to avoid 403 on datacenter IPs.
func setStandardHeaders(req *http.Request) {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36")
	req.Header.Set("Origin", "https://www.youtube.com")
	req.Header.Set("Referer", "https://www.youtube.com/")
}

func (c *InnerTubeClient) ResolveTarget(ctx context.Context, target string) (*InitialState, error) {
	liveUrl := buildLiveURL(target)
	slog.Info("resolving target via InnerTube API", "url", liveUrl)

	// Step 1: resolve_url to find videoId or browseId
	payload := map[string]any{
		"context": c.innertubeContext(),
		"url":     liveUrl,
	}

	b, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, "POST", "https://www.youtube.com/youtubei/v1/navigation/resolve_url?prettyPrint=false", bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	setStandardHeaders(req)

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == 404 {
		return nil, ErrNotFound
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("resolve_url api returned %d", resp.StatusCode)
	}

	var resResp resolveUrlResponse
	if err := json.NewDecoder(resp.Body).Decode(&resResp); err != nil {
		return nil, err
	}

	videoId := resResp.Endpoint.WatchEndpoint.VideoId
	browseId := resResp.Endpoint.BrowseEndpoint.BrowseId
	browseParams := resResp.Endpoint.BrowseEndpoint.Params

	slog.Info("resolve_url result", "target", target, "videoId", videoId, "browseId", browseId)

	// If we got a direct watchEndpoint, great — use it
	// If we got a browseEndpoint, call browse API to find the live video
	if videoId == "" && browseId != "" {
		slog.Info("got browseEndpoint, fetching channel page to find live video", "browseId", browseId)
		vid, err := c.findLiveVideoFromBrowse(ctx, browseId, browseParams)
		if err != nil {
			return nil, err
		}
		videoId = vid
	}

	if videoId == "" {
		slog.Error("could not find a live video for target", "target", target)
		return nil, ErrNotLive
	}

	slog.Info("resolved target to live video", "target", target, "videoId", videoId)

	// Step 2: Use 'next' API to get the live chat continuation token
	return c.getChatContinuation(ctx, videoId)
}

// findLiveVideoFromBrowse calls the browse API and looks for a video with a LIVE overlay badge.
func (c *InnerTubeClient) findLiveVideoFromBrowse(ctx context.Context, browseId, params string) (string, error) {
	payload := map[string]any{
		"context":  c.innertubeContext(),
		"browseId": browseId,
	}
	if params != "" {
		payload["params"] = params
	}

	b, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, "POST", "https://www.youtube.com/youtubei/v1/browse?prettyPrint=false", bytes.NewReader(b))
	if err != nil {
		return "", err
	}
	setStandardHeaders(req)

	resp, err := c.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return "", fmt.Errorf("browse api returned %d", resp.StatusCode)
	}

	var browseResp browseResponse
	if err := json.NewDecoder(resp.Body).Decode(&browseResp); err != nil {
		return "", err
	}

	// Search through tabs for a video with a LIVE overlay
	for _, tab := range browseResp.Contents.TwoColumnBrowseResultsRenderer.Tabs {
		for _, item := range tab.TabRenderer.Content.RichGridRenderer.Contents {
			vr := item.RichItemRenderer.Content.VideoRenderer
			if vr.VideoId == "" {
				continue
			}
			for _, overlay := range vr.ThumbnailOverlays {
				if overlay.ThumbnailOverlayTimeStatusRenderer.Style == "LIVE" {
					slog.Info("found live video via browse", "videoId", vr.VideoId)
					return vr.VideoId, nil
				}
			}
		}
	}

	slog.Error("browse response contained no LIVE videos", "browseId", browseId)
	return "", ErrNotLive
}

// getChatContinuation calls the 'next' API to extract the live chat continuation token.
func (c *InnerTubeClient) getChatContinuation(ctx context.Context, videoId string) (*InitialState, error) {
	nextPayload := map[string]any{
		"context": c.innertubeContext(),
		"videoId": videoId,
	}

	nb, _ := json.Marshal(nextPayload)
	nreq, err := http.NewRequestWithContext(ctx, "POST", "https://www.youtube.com/youtubei/v1/next?prettyPrint=false", bytes.NewReader(nb))
	if err != nil {
		return nil, err
	}
	setStandardHeaders(nreq)

	nresp, err := c.client.Do(nreq)
	if err != nil {
		return nil, err
	}
	defer nresp.Body.Close()

	if nresp.StatusCode != 200 {
		return nil, fmt.Errorf("next api returned %d", nresp.StatusCode)
	}

	var nextResp nextResponse
	if err := json.NewDecoder(nresp.Body).Decode(&nextResp); err != nil {
		return nil, err
	}

	conts := nextResp.Contents.TwoColumnWatchNextResults.ConversationBar.LiveChatRenderer.Continuations
	if len(conts) == 0 || conts[0].ReloadContinuationData.Continuation == "" {
		slog.Error("could not find live chat continuation token in next response", "videoId", videoId)
		return nil, errors.New("live chat not available on this stream")
	}

	continuation := conts[0].ReloadContinuationData.Continuation
	slog.Info("successfully resolved initial continuation", "videoId", videoId)

	return &InitialState{
		APIKey:       "",
		Continuation: continuation,
	}, nil
}

func (c *InnerTubeClient) GetLiveChat(ctx context.Context, apiKey, continuation string) (*LiveChatResponse, error) {
	url := "https://www.youtube.com/youtubei/v1/live_chat/get_live_chat?prettyPrint=false"
	if apiKey != "" {
		url += "&key=" + apiKey
	}

	payload := map[string]any{
		"context":      c.innertubeContext(),
		"continuation": continuation,
	}

	b, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	setStandardHeaders(req)

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		bodySnippet, _ := io.ReadAll(io.LimitReader(resp.Body, 500))
		slog.Error("get_live_chat non-200", "status", resp.StatusCode, "body", string(bodySnippet))
		return nil, fmt.Errorf("api returned %d", resp.StatusCode)
	}

	var chatResp LiveChatResponse
	if err := json.NewDecoder(resp.Body).Decode(&chatResp); err != nil {
		return nil, err
	}

	return &chatResp, nil
}
