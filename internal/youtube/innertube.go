package youtube

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

var (
	ErrNotLive     = errors.New("channel or video is not currently live")
	ErrNotFound    = errors.New("target not found")
	ytInitialDataRe = regexp.MustCompile(`ytInitialData\s*=\s*({.+?});\s*</script>`)
)

type InnerTubeClient struct {
	client *http.Client
}

func NewClient() *InnerTubeClient {
	return &InnerTubeClient{
		client: &http.Client{Timeout: 10 * time.Second},
	}
}

func (c *InnerTubeClient) ResolveTarget(ctx context.Context, target string) (*InitialState, error) {
	url := "https://www.youtube.com/watch?v=" + target
	if strings.HasPrefix(target, "@") || strings.HasPrefix(target, "UC") {
		url = "https://www.youtube.com/" + target + "/live"
	}

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == 404 {
		return nil, ErrNotFound
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	matches := ytInitialDataRe.FindSubmatch(body)
	if len(matches) < 2 {
		return nil, errors.New("could not find ytInitialData in page")
	}
	initialDataStr := string(matches[1])

	apiKeyRe := regexp.MustCompile(`"INNERTUBE_API_KEY":"([^"]+)"`)
	apiMatches := apiKeyRe.FindSubmatch(body)
	apiKey := ""
	if len(apiMatches) >= 2 {
		apiKey = string(apiMatches[1])
	}

	continuationBlockMatch := regexp.MustCompile(`"liveChatRenderer":\s*{[^}]+?"continuation":"([^"]+)"`).FindSubmatch(matches[1])
	if len(continuationBlockMatch) < 2 {
		if strings.Contains(initialDataStr, "paddingContinuationData") {
            // Might be inside timedContinuationData but further nested
            continuationBlockMatch = regexp.MustCompile(`"continuation":"([^"]+)"`).FindSubmatch([]byte(initialDataStr))
        }
	}
	
	if len(continuationBlockMatch) >= 2 {
		return &InitialState{
			APIKey:       apiKey,
			Continuation: string(continuationBlockMatch[1]),
		}, nil
	}
	
	return nil, ErrNotLive
}

func (c *InnerTubeClient) GetLiveChat(ctx context.Context, apiKey, continuation string) (*LiveChatResponse, error) {
	url := "https://www.youtube.com/youtubei/v1/live_chat/get_live_chat?prettyPrint=false"
	if apiKey != "" {
		url += "&key=" + apiKey
	}

	payload := map[string]any{
		"context": map[string]any{
			"client": map[string]string{
				"clientName": "WEB",
				"clientVersion": "2.20240101.00.00",
			},
		},
		"continuation": continuation,
	}
	
	b, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("api returned %d", resp.StatusCode)
	}

	var chatResp LiveChatResponse
	if err := json.NewDecoder(resp.Body).Decode(&chatResp); err != nil {
		return nil, err
	}

	return &chatResp, nil
}
