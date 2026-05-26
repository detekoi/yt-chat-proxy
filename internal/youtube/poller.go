package youtube

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/detekoi/yt-chat-proxy/internal/hub"
)

// isUnicodeEmoji checks whether an emojiId string is a native unicode emoji
// rather than a YouTube custom emote identifier. YouTube sends all emoji
// (including standard unicode) with image thumbnails, so we need to
// distinguish them by checking if the leading rune falls in emoji Unicode blocks.
func isUnicodeEmoji(emojiId string) bool {
	if emojiId == "" {
		return false
	}
	firstRune := []rune(emojiId)[0]
	return firstRune >= 0x1F000 || // Supplemental area (most emoji)
		unicode.Is(unicode.So, firstRune) || // Symbol, other (©, ®, ☀, ☎, etc.)
		(firstRune >= 0x2300 && firstRune <= 0x23FF) || // Misc Technical (⌚, ⏰)
		(firstRune >= 0x2600 && firstRune <= 0x27BF) || // Misc symbols + Dingbats
		(firstRune >= 0x2B50 && firstRune <= 0x2B55) || // Stars, circles
		(firstRune >= 0x3030 && firstRune <= 0x303D) // CJK symbols used as emoji
}

// getEmoteLabel extracts a human-readable label for a YouTube custom emote.
// Priority: accessibility label > first shortcut (colons stripped) > emojiId.
func getEmoteLabel(emoji *EmojiRun) string {
	if emoji.Image.Accessibility != nil {
		label := emoji.Image.Accessibility.AccessibilityData.Label
		if label != "" {
			return label
		}
	}
	if len(emoji.Shortcuts) > 0 {
		return strings.Trim(emoji.Shortcuts[0], ":")
	}
	return emoji.EmojiId
}

type PollerManager struct {
	mu      sync.Mutex
	pollers map[string]context.CancelFunc
	client  *InnerTubeClient
	hub     *hub.Hub
}

func NewPollerManager(h *hub.Hub) *PollerManager {
	return &PollerManager{
		pollers: make(map[string]context.CancelFunc),
		client:  NewClient(),
		hub:     h,
	}
}

func (m *PollerManager) StartPoller(target string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.pollers[target]; exists {
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	m.pollers[target] = cancel

	go m.runPoller(ctx, target)
}

func (m *PollerManager) StopPoller(target string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if cancel, exists := m.pollers[target]; exists {
		cancel()
		delete(m.pollers, target)
	}
}

func (m *PollerManager) runPoller(ctx context.Context, target string) {
	seenIDs := make(map[string]bool) // Persists across re-resolution cycles to avoid replaying old messages

	for {
		state := m.resolveTargetWithRetries(ctx, target)
		if state == nil {
			return // gave up, error message sent, or context cancelled
		}

		shouldReResolve := m.pollStream(ctx, target, state, seenIDs)
		if !shouldReResolve {
			return // context cancelled
		}

		slog.Info("poller loop cycle completed, re-resolving target for possible new stream", "target", target)
		select {
		case <-ctx.Done():
			return
		case <-time.After(15 * time.Second):
		}
	}
}

func (m *PollerManager) resolveTargetWithRetries(ctx context.Context, target string) *InitialState {
	const maxResolveRetries = 40      // 40 * 15s = 10 minutes of retrying
	const resolveRetryInterval = 15 * time.Second

	for attempt := 0; attempt <= maxResolveRetries; attempt++ {
		slog.Info("poller resolving target", "target", target, "attempt", attempt)
		state, err := m.client.ResolveTarget(ctx, target)
		if err == nil {
			return state
		}

		slog.Warn("resolving target failed, will retry", "target", target, "err", err, "attempt", attempt)

		// On first failure, notify the client
		if attempt == 0 {
			m.hub.Broadcast(target, map[string]any{
				"type":    "system",
				"message": "Waiting for YouTube stream to go live...",
			})
		}

		// If we've exhausted retries, give up
		if attempt == maxResolveRetries {
			slog.Error("giving up resolving target after max retries", "target", target)
			m.hub.Broadcast(target, map[string]any{
				"type":    "system",
				"message": "Could not find a live stream. Please check the channel name and try again.",
			})
			m.StopPoller(target)
			return nil
		}

		// Wait before retrying, but respect cancellation
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(resolveRetryInterval):
		}
	}
	return nil
}

func (m *PollerManager) pollStream(ctx context.Context, target string, state *InitialState, seenIDs map[string]bool) bool {
	continuation := state.Continuation
	apiKey := state.APIKey

	m.hub.Broadcast(target, map[string]any{
		"type":    "system",
		"status":  "connected",
		"message": "Connected to YouTube stream.",
	})

	const maxConsecutiveErrors = 10
	consecutiveErrors := 0
	lastMessageTime := time.Now()

	for {
		select {
		case <-ctx.Done():
			slog.Info("poller stopped", "target", target)
			return false
		default:
		}

		resp, err := m.client.GetLiveChat(ctx, apiKey, continuation)
		if err != nil {
			consecutiveErrors++
			slog.Error("get live chat err", "target", target, "err", err, "consecutiveErrors", consecutiveErrors)
			if consecutiveErrors >= maxConsecutiveErrors {
				slog.Error("too many consecutive poll errors, stopping poller", "target", target, "consecutiveErrors", consecutiveErrors)
				m.hub.Broadcast(target, map[string]any{
					"type":    "system",
					"message": "Lost connection to YouTube stream. Reconnecting...",
				})
				return true // re-resolve
			}
			time.Sleep(5 * time.Second)
			continue
		}
		consecutiveErrors = 0 // Reset on success

		actions := resp.ContinuationContents.LiveChatContinuation.Actions
		if len(actions) > 0 {
			lastMessageTime = time.Now()
		}

		for _, action := range actions {
			if action.AddChatItemAction != nil {
				jsonMsg := m.normalizeAction(action.AddChatItemAction)
				if jsonMsg != nil {
					msgId, _ := jsonMsg["id"].(string)
					if msgId != "" && seenIDs[msgId] {
						continue // Already sent this message
					}
					if msgId != "" {
						seenIDs[msgId] = true
					}
					jsonMsg["platform"] = "youtube"
					m.hub.Broadcast(target, jsonMsg)
				}
			}
		}

		// Cap seenIDs to prevent unbounded growth on very long streams
		if len(seenIDs) > 5000 {
			seenIDs = make(map[string]bool)
		}

		conts := resp.ContinuationContents.LiveChatContinuation.Continuations
		timeoutMs := 3000
		hasContinuation := false

		if len(conts) > 0 {
			if conts[0].TimedContinuationData != nil {
				continuation = conts[0].TimedContinuationData.Continuation
				hasContinuation = true
				if conts[0].TimedContinuationData.TimeoutMs > 0 {
					timeoutMs = conts[0].TimedContinuationData.TimeoutMs
				}
			} else if conts[0].InvalidationContinuationData != nil {
				continuation = conts[0].InvalidationContinuationData.Continuation
				hasContinuation = true
				if conts[0].InvalidationContinuationData.TimeoutMs > 0 {
					timeoutMs = conts[0].InvalidationContinuationData.TimeoutMs
				}
			}
		}

		if !hasContinuation || continuation == "" {
			slog.Info("no continuation found, stream ended", "target", target)
			m.hub.Broadcast(target, map[string]any{
				"type":    "system",
				"message": "YouTube stream appears to have ended. Reconnecting...",
			})
			return true // re-resolve
		}

		// Idle detection: if we haven't seen a message for 30 minutes, re-resolve in case the video ID changed or became stale
		if time.Since(lastMessageTime) > 30*time.Minute {
			slog.Info("no messages received for 30 minutes, re-resolving", "target", target)
			return true // re-resolve
		}

		select {
		case <-ctx.Done():
			return false
		case <-time.After(time.Duration(timeoutMs) * time.Millisecond):
		}
	}
}


func (m *PollerManager) normalizeAction(action *AddChatItemAction) map[string]any {
	var r *LiveChatRenderer
	eventType := "chat"

	if action.Item.LiveChatTextMessageRenderer != nil {
		r = action.Item.LiveChatTextMessageRenderer
	} else if action.Item.LiveChatPaidMessageRenderer != nil {
		r = action.Item.LiveChatPaidMessageRenderer
		eventType = "superchat"
	} else if action.Item.LiveChatPaidStickerRenderer != nil {
		r = action.Item.LiveChatPaidStickerRenderer
		eventType = "supersticker"
	} else if action.Item.LiveChatMembershipItemRenderer != nil {
		r = action.Item.LiveChatMembershipItemRenderer
		eventType = "membership"
	} else {
		return nil
	}

	messageStr := ""
	emotes := make(map[string][]string)
	var emoteFragments []map[string]any
	hasCustomEmotes := false
	pendingText := ""

	currentIndex := 0
	for _, run := range r.Message.Runs {
		if run.Text != "" {
			messageStr += run.Text
			pendingText += run.Text
			currentIndex += len(run.Text)
		} else if run.Emoji != nil {
			if isUnicodeEmoji(run.Emoji.EmojiId) {
				messageStr += run.Emoji.EmojiId
				pendingText += run.Emoji.EmojiId
				currentIndex += len(run.Emoji.EmojiId)
			} else if len(run.Emoji.Image.Thumbnails) > 0 {
				hasCustomEmotes = true
				emojiText := " "
				emoteId := run.Emoji.Image.Thumbnails[0].Url
				pos := fmt.Sprintf("%d-%d", currentIndex, currentIndex+len(emojiText)-1)
				emotes[emoteId] = append(emotes[emoteId], pos)
				messageStr += emojiText
				currentIndex += len(emojiText)

				if pendingText != "" {
					emoteFragments = append(emoteFragments, map[string]any{
						"type": "text",
						"text": pendingText,
					})
					pendingText = ""
				}

				label := getEmoteLabel(run.Emoji)
				shortcutText := ""
				if len(run.Emoji.Shortcuts) > 0 {
					shortcutText = run.Emoji.Shortcuts[0]
				}
				emoteFragments = append(emoteFragments, map[string]any{
					"type":     "yt_emote",
					"text":     shortcutText,
					"imageUrl": emoteId,
					"label":    label,
				})
			} else if run.Emoji.EmojiId != "" {
				messageStr += run.Emoji.EmojiId
				pendingText += run.Emoji.EmojiId
				currentIndex += len(run.Emoji.EmojiId)
			}
		}
	}

	if hasCustomEmotes && pendingText != "" {
		emoteFragments = append(emoteFragments, map[string]any{
			"type": "text",
			"text": pendingText,
		})
	}

    tags := map[string]any{}
    var badgeList []string
    
    // Simplistic mapping for standard badges
    for _, b := range r.AuthorBadges {
        if b.LiveChatAuthorBadgeRenderer.Icon != nil {
            val := b.LiveChatAuthorBadgeRenderer.Icon.IconType
            badgeId := "verified/1"
            if val == "OWNER" { badgeId = "broadcaster/1" }
            if val == "MODERATOR" { badgeId = "moderator/1" }
            if val != "" {
                badgeList = append(badgeList, badgeId)
            }
        }
    }
    
    if len(badgeList) > 0 {
        tags["badges"] = strings.Join(badgeList, ",")
    }

    // Prepare rich data
    var amount string
    var subtext string
    var color1, color2 string

    if r.PurchaseAmountText.SimpleText != "" {
        amount = r.PurchaseAmountText.SimpleText
    }
    if r.HeaderSubtext.SimpleText != "" {
        subtext = r.HeaderSubtext.SimpleText
    }

    if r.BodyBackgroundColor != 0 {
        // Unpack ARGB to rgba()
        c := r.BodyBackgroundColor
        color1 = fmt.Sprintf("rgba(%d,%d,%d,%.2f)", (c>>16)&0xFF, (c>>8)&0xFF, c&0xFF, float64((c>>24)&0xFF)/255.0)
    }
    if r.HeaderBackgroundColor != 0 {
        c := r.HeaderBackgroundColor
        color2 = fmt.Sprintf("rgba(%d,%d,%d,%.2f)", (c>>16)&0xFF, (c>>8)&0xFF, c&0xFF, float64((c>>24)&0xFF)/255.0)
    }

	result := map[string]any{
		"type":        "message",
		"eventType":   eventType,
		"username":    r.AuthorName.SimpleText,
		"message":     messageStr,
		"emotes":      emotes,
        "tags":        tags,
		"id":          r.Id,
		"channelId":   r.AuthorExternalChannelId,
        "amount":      amount,
        "subtext":     subtext,
        "bodyColor":   color1,
        "headerColor": color2,
	}

	// Only include emoteFragments when custom emotes are present
	if hasCustomEmotes && len(emoteFragments) > 0 {
		result["emoteFragments"] = emoteFragments
	}

	return result
}
