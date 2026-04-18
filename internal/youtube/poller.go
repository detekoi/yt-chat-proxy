package youtube

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/detekoi/yt-chat-proxy/internal/hub"
)

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
	const maxResolveRetries = 40      // 40 * 15s = 10 minutes of retrying
	const resolveRetryInterval = 15 * time.Second

	var state *InitialState
	for attempt := 0; attempt <= maxResolveRetries; attempt++ {
		slog.Info("poller resolving target", "target", target, "attempt", attempt)
		var err error
		state, err = m.client.ResolveTarget(ctx, target)
		if err == nil {
			break // Success
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
			return
		}

		// Wait before retrying, but respect cancellation
		select {
		case <-ctx.Done():
			return
		case <-time.After(resolveRetryInterval):
		}
	}

	continuation := state.Continuation
	apiKey := state.APIKey
	seenIDs := make(map[string]bool) // Deduplicate across poll cycles

	m.hub.Broadcast(target, map[string]any{
		"type":    "system",
		"status":  "connected",
		"message": "Connected to YouTube stream.",
	})

	for {
		select {
		case <-ctx.Done():
			slog.Info("poller stopped", "target", target)
			return
		default:
		}

		resp, err := m.client.GetLiveChat(ctx, apiKey, continuation)
		if err != nil {
			slog.Error("get live chat err", "target", target, "err", err)
			time.Sleep(5 * time.Second)
			continue
		}

		actions := resp.ContinuationContents.LiveChatContinuation.Actions
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
		if len(conts) > 0 {
			if conts[0].TimedContinuationData != nil {
				continuation = conts[0].TimedContinuationData.Continuation
				if conts[0].TimedContinuationData.TimeoutMs > 0 {
					timeoutMs = conts[0].TimedContinuationData.TimeoutMs
				}
			} else if conts[0].InvalidationContinuationData != nil {
				continuation = conts[0].InvalidationContinuationData.Continuation
				if conts[0].InvalidationContinuationData.TimeoutMs > 0 {
					timeoutMs = conts[0].InvalidationContinuationData.TimeoutMs
				}
			}
		}

		select {
		case <-ctx.Done():
			return
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
	
	currentIndex := 0
	for _, run := range r.Message.Runs {
		if run.Text != "" {
			messageStr += run.Text
			currentIndex += len(run.Text) // simple length approximation
		} else if run.Emoji != nil {
			emojiText := " "
			if len(run.Emoji.Image.Thumbnails) > 0 {
				url := run.Emoji.Image.Thumbnails[0].Url
				emoteId := url 
				pos := fmt.Sprintf("%d-%d", currentIndex, currentIndex+len(emojiText)-1)
				emotes[emoteId] = append(emotes[emoteId], pos)
			}
			messageStr += emojiText
			currentIndex += len(emojiText)
		}
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

	return map[string]any{
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
}
