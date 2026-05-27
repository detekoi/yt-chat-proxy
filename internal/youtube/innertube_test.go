package youtube

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestBuildLiveURL(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"@Parfaitfair", "https://www.youtube.com/@Parfaitfair/live"},
		{"UC1234567890", "https://www.youtube.com/UC1234567890/live"},
		{"abc", "https://www.youtube.com/@abc/live"},
		// 11 chars containing lowercase only -> treated as handle
		{"abcdefghijk", "https://www.youtube.com/@abcdefghijk/live"},
		// 11 chars containing mixed case -> video ID
		{"AbCdEfGhIjK", "https://www.youtube.com/watch?v=AbCdEfGhIjK"},
		{"abCdEfGhIj1", "https://www.youtube.com/watch?v=abCdEfGhIj1"},
		// Full URLs
		{"https://www.youtube.com/watch?v=xyz12345678", "https://www.youtube.com/watch?v=xyz12345678"},
		{"https://youtu.be/xyz12345678", "https://www.youtube.com/watch?v=xyz12345678"},
		{"https://www.youtube.com/live/xyz12345678", "https://www.youtube.com/watch?v=xyz12345678"},
		{"https://www.youtube.com/@Parfaitfair/live", "https://www.youtube.com/@Parfaitfair/live"},
	}

	for _, tc := range tests {
		got := buildLiveURL(tc.input)
		if got != tc.expected {
			t.Errorf("buildLiveURL(%q) = %q; want %q", tc.input, got, tc.expected)
		}
	}
}

type mockRoundTripper func(req *http.Request) (*http.Response, error)

func (m mockRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	return m(req)
}

func TestResolveTarget_WatchEndpoint(t *testing.T) {
	httpClient := &http.Client{
		Transport: mockRoundTripper(func(req *http.Request) (*http.Response, error) {
			path := req.URL.Path
			var body string

			if strings.Contains(path, "resolve_url") {
				body = `{
					"endpoint": {
						"watchEndpoint": {
							"videoId": "watch-vid-123"
						}
					}
				}`
			} else if strings.Contains(path, "next") {
				body = `{
					"contents": {
						"twoColumnWatchNextResults": {
							"conversationBar": {
								"liveChatRenderer": {
									"continuations": [
										{
											"reloadContinuationData": {
												"continuation": "watch-continuation-token"
											}
										}
									]
								}
							}
						}
					}
				}`
			} else {
				return nil, io.ErrUnexpectedEOF
			}

			return &http.Response{
				StatusCode: 200,
				Body:       io.NopCloser(bytes.NewBufferString(body)),
				Header:     make(http.Header),
			}, nil
		}),
	}

	client := NewClientWithHTTPClient(httpClient)
	state, err := client.ResolveTarget(context.Background(), "watch-vid-123")
	if err != nil {
		t.Fatalf("unexpected error resolving target: %v", err)
	}

	if state == nil {
		t.Fatal("expected non-nil state")
	}

	if state.Continuation != "watch-continuation-token" {
		t.Errorf("expected continuation 'watch-continuation-token', got %q", state.Continuation)
	}
}

func TestResolveTarget_BrowseEndpoint(t *testing.T) {
	httpClient := &http.Client{
		Transport: mockRoundTripper(func(req *http.Request) (*http.Response, error) {
			path := req.URL.Path
			var body string

			if strings.Contains(path, "resolve_url") {
				body = `{
					"endpoint": {
						"browseEndpoint": {
							"browseId": "UC-channel-id",
							"params": "some-params"
						}
					}
				}`
			} else if strings.Contains(path, "browse") {
				body = `{
					"contents": {
						"twoColumnBrowseResultsRenderer": {
							"tabs": [
								{
									"tabRenderer": {
										"content": {
											"richGridRenderer": {
												"contents": [
													{
														"richItemRenderer": {
															"content": {
																"videoRenderer": {
																	"videoId": "browse-live-vid",
																	"thumbnailOverlays": [
																		{
																			"thumbnailOverlayTimeStatusRenderer": {
																				"style": "LIVE"
																			}
																		}
																	]
																}
															}
														}
													}
												]
											}
										}
									}
								}
							]
						}
					}
				}`
			} else if strings.Contains(path, "next") {
				body = `{
					"contents": {
						"twoColumnWatchNextResults": {
							"conversationBar": {
								"liveChatRenderer": {
									"continuations": [
										{
											"reloadContinuationData": {
												"continuation": "browse-continuation-token"
											}
										}
									]
								}
							}
						}
					}
				}`
			} else {
				return nil, io.ErrUnexpectedEOF
			}

			return &http.Response{
				StatusCode: 200,
				Body:       io.NopCloser(bytes.NewBufferString(body)),
				Header:     make(http.Header),
			}, nil
		}),
	}

	client := NewClientWithHTTPClient(httpClient)
	state, err := client.ResolveTarget(context.Background(), "@Parfaitfair")
	if err != nil {
		t.Fatalf("unexpected error resolving target: %v", err)
	}

	if state == nil {
		t.Fatal("expected non-nil state")
	}

	if state.Continuation != "browse-continuation-token" {
		t.Errorf("expected continuation 'browse-continuation-token', got %q", state.Continuation)
	}
}

func TestResolveTarget_NotFoundAndNotLive(t *testing.T) {
	t.Run("NotFound", func(t *testing.T) {
		httpClient := &http.Client{
			Transport: mockRoundTripper(func(req *http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: 404,
					Body:       io.NopCloser(bytes.NewBufferString("not found")),
					Header:     make(http.Header),
				}, nil
			}),
		}

		client := NewClientWithHTTPClient(httpClient)
		_, err := client.ResolveTarget(context.Background(), "missing-target")
		if err != ErrNotFound {
			t.Errorf("expected ErrNotFound, got %v", err)
		}
	})

	t.Run("NotLive", func(t *testing.T) {
		httpClient := &http.Client{
			Transport: mockRoundTripper(func(req *http.Request) (*http.Response, error) {
				path := req.URL.Path
				var body string

				if strings.Contains(path, "resolve_url") {
					body = `{
						"endpoint": {
							"browseEndpoint": {
								"browseId": "UC-channel-id"
							}
						}
					}`
				} else if strings.Contains(path, "browse") {
					// Video has "DEFAULT" or other thumbnail overlay style, not "LIVE"
					body = `{
						"contents": {
							"twoColumnBrowseResultsRenderer": {
								"tabs": [
									{
										"tabRenderer": {
											"content": {
												"richGridRenderer": {
													"contents": [
														{
															"richItemRenderer": {
																"content": {
																	"videoRenderer": {
																		"videoId": "offline-vid",
																		"thumbnailOverlays": [
																			{
																				"thumbnailOverlayTimeStatusRenderer": {
																					"style": "DEFAULT"
																				}
																			}
																		]
																	}
																}
															}
														}
													]
												}
											}
										}
									}
								]
							}
						}
					}`
				}

				return &http.Response{
					StatusCode: 200,
					Body:       io.NopCloser(bytes.NewBufferString(body)),
					Header:     make(http.Header),
				}, nil
			}),
		}

		client := NewClientWithHTTPClient(httpClient)
		_, err := client.ResolveTarget(context.Background(), "@offline-user")
		if err != ErrNotLive {
			t.Errorf("expected ErrNotLive, got %v", err)
		}
	})
}

func TestGetLiveChat(t *testing.T) {
	httpClient := &http.Client{
		Transport: mockRoundTripper(func(req *http.Request) (*http.Response, error) {
			body := `{
				"continuationContents": {
					"liveChatContinuation": {
						"actions": [
							{
								"addChatItemAction": {
									"item": {
										"liveChatTextMessageRenderer": {
											"id": "msg-123",
											"authorName": { "simpleText": "Alice" }
										}
									}
								}
							}
						],
						"continuations": [
							{
								"timedContinuationData": {
									"timeoutMs": 3500,
									"continuation": "next-token-abc"
								}
							}
						]
					}
				}
			}`

			return &http.Response{
				StatusCode: 200,
				Body:       io.NopCloser(bytes.NewBufferString(body)),
				Header:     make(http.Header),
			}, nil
		}),
	}

	client := NewClientWithHTTPClient(httpClient)
	resp, err := client.GetLiveChat(context.Background(), "api-key", "continuation-token")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	actions := resp.ContinuationContents.LiveChatContinuation.Actions
	if len(actions) != 1 {
		t.Fatalf("expected 1 action, got %d", len(actions))
	}

	msgRenderer := actions[0].AddChatItemAction.Item.LiveChatTextMessageRenderer
	if msgRenderer.Id != "msg-123" || msgRenderer.AuthorName.SimpleText != "Alice" {
		t.Errorf("unexpected message data: %v", msgRenderer)
	}

	conts := resp.ContinuationContents.LiveChatContinuation.Continuations
	if len(conts) != 1 || conts[0].TimedContinuationData.Continuation != "next-token-abc" || conts[0].TimedContinuationData.TimeoutMs != 3500 {
		t.Errorf("unexpected continuation data: %v", conts)
	}
}
