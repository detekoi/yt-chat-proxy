package youtube

import (
	"bytes"
	"io"
	"net/http"
	"testing"

	"github.com/detekoi/yt-chat-proxy/internal/hub"
)

func TestIsUnicodeEmoji(t *testing.T) {
	tests := []struct {
		input    string
		expected bool
	}{
		{"🍉", true},
		{"😀", true},
		{"©", true},
		{"☀", true},
		{"⏰", true},
		{"⭐", true},
		{"", false},
		{"custom_emote", false},
		{":shortcut:", false},
		{"A", false},
	}

	for _, tc := range tests {
		got := isUnicodeEmoji(tc.input)
		if got != tc.expected {
			t.Errorf("isUnicodeEmoji(%q) = %t; want %t", tc.input, got, tc.expected)
		}
	}
}

func TestGetEmoteLabel(t *testing.T) {
	t.Run("AccessibilityLabel", func(t *testing.T) {
		emoji := &EmojiRun{}
		emoji.Image.Accessibility = &EmojiAccessibility{}
		emoji.Image.Accessibility.AccessibilityData.Label = "cute_cat"
		emoji.Shortcuts = []string{":cat:"}
		emoji.EmojiId = "id-cat"

		got := getEmoteLabel(emoji)
		if got != "cute_cat" {
			t.Errorf("expected 'cute_cat', got %q", got)
		}
	})

	t.Run("ShortcutFallback", func(t *testing.T) {
		emoji := &EmojiRun{}
		emoji.Shortcuts = []string{":cat:"}
		emoji.EmojiId = "id-cat"

		got := getEmoteLabel(emoji)
		if got != "cat" {
			t.Errorf("expected 'cat', got %q", got)
		}
	})

	t.Run("EmojiIdFallback", func(t *testing.T) {
		emoji := &EmojiRun{}
		emoji.EmojiId = "id-cat"

		got := getEmoteLabel(emoji)
		if got != "id-cat" {
			t.Errorf("expected 'id-cat', got %q", got)
		}
	})
}

func TestNormalizeAction(t *testing.T) {
	h := hub.New(nil, nil)
	pm := NewPollerManager(h)

	t.Run("TextMessage", func(t *testing.T) {
		action := &AddChatItemAction{}
		r := &LiveChatRenderer{}
		r.Id = "msg-1"
		r.AuthorName.SimpleText = "Alice"
		r.AuthorExternalChannelId = "UC-alice"
		r.Message.Runs = []Run{
			{Text: "Hello "},
			{Text: "World!"},
		}
		action.Item.LiveChatTextMessageRenderer = r

		result := pm.normalizeAction(action)
		if result == nil {
			t.Fatal("expected non-nil result")
		}

		if result["type"] != "message" || result["eventType"] != "chat" {
			t.Errorf("unexpected type or eventType: %v", result)
		}
		if result["username"] != "Alice" || result["message"] != "Hello World!" {
			t.Errorf("unexpected username or message: %v", result)
		}
		if result["id"] != "msg-1" || result["channelId"] != "UC-alice" {
			t.Errorf("unexpected id or channelId: %v", result)
		}
	})

	t.Run("PaidMessage_SuperChat", func(t *testing.T) {
		action := &AddChatItemAction{}
		r := &LiveChatRenderer{}
		r.Id = "sc-1"
		r.AuthorName.SimpleText = "Bob"
		r.Message.Runs = []Run{{Text: "Keep up the good work!"}}
		r.PurchaseAmountText.SimpleText = "$50.00"
		r.BodyBackgroundColor = 0xFF112233   // ARGB: A=255, R=17, G=34, B=51
		r.HeaderBackgroundColor = 0x80445566 // ARGB: A=128, R=68, G=85, B=102
		action.Item.LiveChatPaidMessageRenderer = r

		result := pm.normalizeAction(action)
		if result == nil {
			t.Fatal("expected non-nil result")
		}

		if result["eventType"] != "superchat" || result["amount"] != "$50.00" {
			t.Errorf("unexpected eventType or amount: %v", result)
		}
		if result["bodyColor"] != "rgba(17,34,51,1.00)" {
			t.Errorf("expected bodyColor rgba(17,34,51,1.00), got %q", result["bodyColor"])
		}
		if result["headerColor"] != "rgba(68,85,102,0.50)" {
			t.Errorf("expected headerColor rgba(68,85,102,0.50), got %q", result["headerColor"])
		}
	})

	t.Run("PaidSticker_SuperSticker", func(t *testing.T) {
		action := &AddChatItemAction{}
		r := &LiveChatRenderer{}
		r.Id = "sticker-1"
		r.AuthorName.SimpleText = "Charlie"
		r.PurchaseAmountText.SimpleText = "$5.00"
		r.BodyBackgroundColor = 0xFFFFAA00
		action.Item.LiveChatPaidStickerRenderer = r

		result := pm.normalizeAction(action)
		if result == nil {
			t.Fatal("expected non-nil result")
		}

		if result["eventType"] != "supersticker" || result["amount"] != "$5.00" {
			t.Errorf("unexpected eventType or amount: %v", result)
		}
	})

	t.Run("MembershipItem", func(t *testing.T) {
		action := &AddChatItemAction{}
		r := &LiveChatRenderer{}
		r.Id = "member-1"
		r.AuthorName.SimpleText = "David"
		r.HeaderSubtext.SimpleText = "New Member"
		r.Message.Runs = []Run{{Text: "David joined the channel!"}}
		action.Item.LiveChatMembershipItemRenderer = r

		result := pm.normalizeAction(action)
		if result == nil {
			t.Fatal("expected non-nil result")
		}

		if result["eventType"] != "membership" || result["subtext"] != "New Member" {
			t.Errorf("unexpected eventType or subtext: %v", result)
		}
	})

	t.Run("MessageWithCustomEmotes", func(t *testing.T) {
		action := &AddChatItemAction{}
		r := &LiveChatRenderer{}
		r.Id = "emote-msg"
		r.AuthorName.SimpleText = "Eve"

		// Message: "Hello :smile: Bye"
		// Custom emote is ":smile:" with url "http://smile.png"
		emoji := &EmojiRun{EmojiId: "smile-id"}
		emoji.Image.Thumbnails = []Thumbnail{{Url: "http://smile.png", Width: 24, Height: 24}}
		emoji.Shortcuts = []string{":smile:"}

		r.Message.Runs = []Run{
			{Text: "Hello "},
			{Emoji: emoji},
			{Text: " Bye"},
		}
		action.Item.LiveChatTextMessageRenderer = r

		result := pm.normalizeAction(action)
		if result == nil {
			t.Fatal("expected non-nil result")
		}

		// Message normalization replaces custom emotes with " " (space)
		expectedMsg := "Hello   Bye"
		if result["message"] != expectedMsg {
			t.Errorf("expected message %q, got %q", expectedMsg, result["message"])
		}

		// Emotes index check:
		// "Hello " is 6 chars, so index starts at 6.
		// The custom emote is replaced by " ", length 1, so index is 6-6.
		emotes, ok := result["emotes"].(map[string][]string)
		if !ok {
			t.Fatalf("expected emotes map, got %T", result["emotes"])
		}
		positions := emotes["http://smile.png"]
		if len(positions) != 1 || positions[0] != "6-6" {
			t.Errorf("expected emote position ['6-6'], got %v", positions)
		}

		// Emote fragments check
		fragments, ok := result["emoteFragments"].([]map[string]any)
		if !ok {
			t.Fatalf("expected emoteFragments slice, got %T", result["emoteFragments"])
		}
		if len(fragments) != 3 {
			t.Fatalf("expected 3 fragments, got %d", len(fragments))
		}
		if fragments[0]["type"] != "text" || fragments[0]["text"] != "Hello " {
			t.Errorf("unexpected fragment 0: %v", fragments[0])
		}
		if fragments[1]["type"] != "yt_emote" || fragments[1]["text"] != ":smile:" || fragments[1]["imageUrl"] != "http://smile.png" {
			t.Errorf("unexpected fragment 1: %v", fragments[1])
		}
		if fragments[2]["type"] != "text" || fragments[2]["text"] != " Bye" {
			t.Errorf("unexpected fragment 2: %v", fragments[2])
		}
	})

	t.Run("MessageWithBadges", func(t *testing.T) {
		action := &AddChatItemAction{}
		r := &LiveChatRenderer{}
		r.Id = "badge-msg"
		r.AuthorName.SimpleText = "Frank"
		r.Message.Runs = []Run{{Text: "Hi"}}
		
		// Add owner and verified badges
		r.AuthorBadges = []struct {
			LiveChatAuthorBadgeRenderer struct {
				Icon *struct {
					IconType string `json:"iconType"`
				} `json:"icon,omitempty"`
			} `json:"liveChatAuthorBadgeRenderer"`
		}{
			{
				LiveChatAuthorBadgeRenderer: struct {
					Icon *struct {
						IconType string `json:"iconType"`
					} `json:"icon,omitempty"`
				}{
					Icon: &struct {
						IconType string `json:"iconType"`
					}{IconType: "OWNER"},
				},
			},
			{
				LiveChatAuthorBadgeRenderer: struct {
					Icon *struct {
						IconType string `json:"iconType"`
					} `json:"icon,omitempty"`
				}{
					Icon: &struct {
						IconType string `json:"iconType"`
					}{IconType: "MODERATOR"},
				},
			},
		}

		action.Item.LiveChatTextMessageRenderer = r
		result := pm.normalizeAction(action)
		if result == nil {
			t.Fatal("expected non-nil result")
		}

		tags, ok := result["tags"].(map[string]any)
		if !ok {
			t.Fatalf("expected tags map, got %T", result["tags"])
		}

		badges, ok := tags["badges"].(string)
		if !ok {
			t.Fatalf("expected badges string in tags, got %v", tags["badges"])
		}

		if badges != "broadcaster/1,moderator/1" {
			t.Errorf("expected badges 'broadcaster/1,moderator/1', got %q", badges)
		}
	})
}

func TestPollerManagerStartStop(t *testing.T) {
	// Use a mock HTTP client that returns an error to avoid network calls and bypass the polling loop
	mockClient := NewClientWithHTTPClient(&http.Client{
		Transport: mockRoundTripper(func(req *http.Request) (*http.Response, error) {
			// Return a 500 error to quickly fail resolution, triggering resolveTargetWithRetries loop which will block on time.After
			return &http.Response{
				StatusCode: 500,
				Body:       io.NopCloser(bytes.NewBufferString("server error")),
				Header:     make(http.Header),
			}, nil
		}),
	})

	h := hub.New(nil, nil)
	pm := NewPollerManagerWithClient(h, mockClient)

	target := "some-channel-handle"
	pm.StartPoller(target)

	// Verify poller entry is created
	pm.mu.Lock()
	entry, exists := pm.pollers[target]
	pm.mu.Unlock()

	if !exists {
		t.Fatal("expected poller to be started")
	}

	if entry.id != 1 {
		t.Errorf("expected poller ID 1, got %d", entry.id)
	}

	// Stop poller
	pm.StopPoller(target)

	// Verify poller entry is deleted
	pm.mu.Lock()
	_, exists = pm.pollers[target]
	pm.mu.Unlock()

	if exists {
		t.Error("expected poller to be stopped and deleted")
	}
}
