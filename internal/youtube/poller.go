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

type pollerEntry struct {
	cancel context.CancelFunc
	id     uint64
	state  *pollerState
}

// PollerStatus is a point-in-time view of one poller, exposed via /health so a
// stuck poller can be diagnosed on a deployed instance without shell access.
type PollerStatus struct {
	Target         string    `json:"target"`
	Phase          string    `json:"phase"` // "resolving" or "polling"
	VideoId        string    `json:"videoId,omitempty"`
	ResolveAttempt int       `json:"resolveAttempt,omitempty"`
	LastError      string    `json:"lastError,omitempty"`
	StartedAt      time.Time `json:"startedAt"`
	LastActivity   time.Time `json:"lastActivity"` // last successful resolve step or poll
	LastMessage    *time.Time `json:"lastMessage,omitempty"`
	Messages       uint64    `json:"messages"`
}

type pollerState struct {
	mu sync.Mutex
	PollerStatus
}

func (s *pollerState) update(fn func(st *PollerStatus)) {
	s.mu.Lock()
	fn(&s.PollerStatus)
	s.mu.Unlock()
}

func (s *pollerState) snapshot() PollerStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.PollerStatus
}

type PollerManager struct {
	mu      sync.Mutex
	pollers map[string]*pollerEntry
	nextID  uint64
	client  *InnerTubeClient
	hub     *hub.Hub
}

func NewPollerManager(h *hub.Hub) *PollerManager {
	return NewPollerManagerWithClient(h, NewClient())
}

func NewPollerManagerWithClient(h *hub.Hub, client *InnerTubeClient) *PollerManager {
	return &PollerManager{
		pollers: make(map[string]*pollerEntry),
		client:  client,
		hub:     h,
	}
}

func (m *PollerManager) StartPoller(target string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.pollers[target]; exists {
		return
	}

	m.nextID++
	id := m.nextID
	ctx, cancel := context.WithCancel(context.Background())
	now := time.Now()
	st := &pollerState{PollerStatus: PollerStatus{Target: target, Phase: "resolving", StartedAt: now, LastActivity: now}}
	m.pollers[target] = &pollerEntry{cancel: cancel, id: id, state: st}

	go m.runPoller(ctx, target, id, st)
}

// IsPolling reports whether a poller for target (already normalized by the hub)
// is currently attached to a live chat, i.e. past the resolve phase.
func (m *PollerManager) IsPolling(target string) bool {
	m.mu.Lock()
	entry, ok := m.pollers[target]
	m.mu.Unlock()
	if !ok {
		return false
	}
	return entry.state.snapshot().Phase == "polling"
}

// Snapshot returns the current status of every active poller.
func (m *PollerManager) Snapshot() []PollerStatus {
	m.mu.Lock()
	entries := make([]*pollerEntry, 0, len(m.pollers))
	for _, e := range m.pollers {
		entries = append(entries, e)
	}
	m.mu.Unlock()

	out := make([]PollerStatus, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.state.snapshot())
	}
	return out
}

func (m *PollerManager) StopPoller(target string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if entry, exists := m.pollers[target]; exists {
		entry.cancel()
		delete(m.pollers, target)
	}
}

func (m *PollerManager) runPoller(ctx context.Context, target string, pollerID uint64, st *pollerState) {
	// When this goroutine exits for any reason, clean up our entry
	// (if we're still the active poller) and restart if subscribers remain.
	defer func() {
		m.mu.Lock()
		entry, exists := m.pollers[target]
		// Only delete if the entry is still ours — a replacement poller
		// may have taken our slot if StopPoller + StartPoller raced.
		if exists && entry.id == pollerID {
			delete(m.pollers, target)
		}
		m.mu.Unlock()

		// If subscribers are still waiting, spawn a fresh poller.
		if m.hub.HasSubscribers(target) {
			slog.Info("poller exited but subscribers remain, restarting", "target", target)
			m.StartPoller(target)
		}
	}()

	seenIDs := make(map[string]bool) // Persists across re-resolution cycles to avoid replaying old messages

	for {
		st.update(func(p *PollerStatus) { p.Phase = "resolving"; p.VideoId = "" })
		state := m.resolveTargetWithRetries(ctx, target, st)
		if state == nil {
			return // gave up, error message sent, or context cancelled
		}

		st.update(func(p *PollerStatus) {
			p.Phase = "polling"
			p.VideoId = state.VideoId
			p.ResolveAttempt = 0
			p.LastError = ""
			p.LastActivity = time.Now()
		})
		shouldReResolve := m.pollStream(ctx, target, state, seenIDs, st)
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

func (m *PollerManager) resolveTargetWithRetries(ctx context.Context, target string, st *pollerState) *InitialState {
	const resolveRetryInterval = 15 * time.Second

	for attempt := 0; ; attempt++ {
		slog.Info("poller resolving target", "target", target, "attempt", attempt)
		state, err := m.client.ResolveTarget(ctx, target)
		if err == nil {
			return state
		}
		st.update(func(p *PollerStatus) { p.ResolveAttempt = attempt + 1; p.LastError = err.Error() })

		slog.Warn("resolving target failed, will retry", "target", target, "err", err, "attempt", attempt)

		// On first failure, notify the client
		if attempt == 0 {
			m.hub.Broadcast(target, map[string]any{
				"type":    "system",
				"message": "Waiting for YouTube stream to go live...",
			})
		} else if attempt%40 == 0 {
			m.hub.Broadcast(target, map[string]any{
				"type":    "system",
				"message": "Still looking for a live stream, will keep retrying...",
			})
		}

		// Wait before retrying, but respect cancellation
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(resolveRetryInterval):
		}
	}
}

func (m *PollerManager) pollStream(ctx context.Context, target string, state *InitialState, seenIDs map[string]bool, st *pollerState) bool {
	continuation := state.Continuation
	apiKey := state.APIKey

	m.hub.Broadcast(target, map[string]any{
		"type":    "system",
		"status":  "connected",
		"message": "Connected to YouTube stream.",
	})

	const maxConsecutiveErrors = 10
	const maxPollIntervalMs = 30_000
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
			st.update(func(p *PollerStatus) { p.LastError = err.Error() })
			slog.Error("get live chat err", "target", target, "err", err, "consecutiveErrors", consecutiveErrors)
			if consecutiveErrors >= maxConsecutiveErrors {
				slog.Error("too many consecutive poll errors, stopping poller", "target", target, "consecutiveErrors", consecutiveErrors)
				m.hub.Broadcast(target, map[string]any{
					"type":    "system",
					"message": "Lost connection to YouTube stream. Reconnecting...",
				})
				return true // re-resolve
			}
			select {
			case <-ctx.Done():
				return false
			case <-time.After(5 * time.Second):
			}
			continue
		}
		consecutiveErrors = 0 // Reset on success

		actions := resp.ContinuationContents.LiveChatContinuation.Actions
		if len(actions) > 0 {
			lastMessageTime = time.Now()
		}
		st.update(func(p *PollerStatus) {
			p.LastActivity = time.Now()
			p.LastError = ""
			if len(actions) > 0 {
				t := lastMessageTime
				p.LastMessage = &t
				p.Messages += uint64(len(actions))
			}
		})

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

		// Never trust YouTube for how long to sleep: a huge timeoutMs would park this
		// poller (and every overlay subscribed to it) with no way to recover.
		if timeoutMs > maxPollIntervalMs {
			slog.Warn("clamping oversized poll interval", "target", target, "timeoutMs", timeoutMs)
			timeoutMs = maxPollIntervalMs
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
