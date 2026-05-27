package hub

import (
	"sync"
	"testing"
	"time"
)

func TestNormalizeTarget(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"@Parfaitfair", "parfaitfair"},
		{"@parfaitfair", "parfaitfair"},
		{"parfaitfair", "parfaitfair"},
		{"@Channel-Name123", "Channel-Name123"}, // contains non-letters -> case preserved
		{"videoId12345", "videoId12345"},         // contains digit -> case preserved
		{"VIDEO_ID_ABC", "VIDEO_ID_ABC"},         // contains underscore -> case preserved
		{"VIDEO-ID-ABC", "VIDEO-ID-ABC"},         // contains hyphen -> case preserved
		{"onlyletters", "onlyletters"},           // only letters -> lowercased
		{"ONLYLETTERS", "onlyletters"},           // only letters -> lowercased
	}

	for _, tc := range tests {
		got := normalizeTarget(tc.input)
		if got != tc.expected {
			t.Errorf("normalizeTarget(%q) = %q; want %q", tc.input, got, tc.expected)
		}
	}
}

func TestHubSubscriptionAndBroadcast(t *testing.T) {
	var startMu sync.Mutex
	var stopMu sync.Mutex
	var startedTargets []string
	var stoppedTargets []string

	h := New(
		func(target string) {
			startMu.Lock()
			startedTargets = append(startedTargets, target)
			startMu.Unlock()
		},
		func(target string) {
			stopMu.Lock()
			stoppedTargets = append(stoppedTargets, target)
			stopMu.Unlock()
		},
	)
	h.GracePeriod = 5 * time.Millisecond

	c1 := NewClient(h, nil)
	h.AddClient(c1)

	// Subscribe c1 to a target
	h.Subscribe(c1, "targetA")

	startMu.Lock()
	if len(startedTargets) != 1 || startedTargets[0] != "targeta" {
		t.Errorf("expected startPoller to be called for targeta, got %v", startedTargets)
	}
	startMu.Unlock()

	if !h.HasSubscribers("targetA") {
		t.Error("expected targetA to have subscribers")
	}

	// Broadcast message
	msg := "test-message"
	h.Broadcast("targetA", msg)

	select {
	case gotMsg := <-c1.send:
		if gotMsg != msg {
			t.Errorf("expected broadcast message %v, got %v", msg, gotMsg)
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("timeout waiting for broadcast message")
	}

	// Remove client
	h.RemoveClient(c1)

	// Wait for grace period
	time.Sleep(15 * time.Millisecond)

	stopMu.Lock()
	if len(stoppedTargets) != 1 || stoppedTargets[0] != "targeta" {
		t.Errorf("expected stopPoller to be called for targeta, got %v", stoppedTargets)
	}
	stopMu.Unlock()

	if h.HasSubscribers("targetA") {
		t.Error("expected targetA to have no subscribers")
	}
}

func TestHubGracePeriod(t *testing.T) {
	var stopMu sync.Mutex
	var stoppedTargets []string

	h := New(
		nil,
		func(target string) {
			stopMu.Lock()
			stoppedTargets = append(stoppedTargets, target)
			stopMu.Unlock()
		},
	)
	h.GracePeriod = 20 * time.Millisecond

	c1 := NewClient(h, nil)
	h.AddClient(c1)
	h.Subscribe(c1, "targetGrace")

	// Disconnect client
	h.RemoveClient(c1)

	// Verify stopPoller is not called immediately
	stopMu.Lock()
	if len(stoppedTargets) != 0 {
		t.Error("stopPoller was called immediately, ignoring grace period")
	}
	stopMu.Unlock()

	// Wait for grace period to expire
	time.Sleep(35 * time.Millisecond)

	stopMu.Lock()
	if len(stoppedTargets) != 1 || stoppedTargets[0] != "targetgrace" {
		t.Errorf("expected stopPoller to be called after grace period, got %v", stoppedTargets)
	}
	stopMu.Unlock()
}

func TestHubGracePeriodReconnect(t *testing.T) {
	var stopMu sync.Mutex
	var stoppedTargets []string

	h := New(
		nil,
		func(target string) {
			stopMu.Lock()
			stoppedTargets = append(stoppedTargets, target)
			stopMu.Unlock()
		},
	)
	h.GracePeriod = 30 * time.Millisecond

	c1 := NewClient(h, nil)
	h.AddClient(c1)
	h.Subscribe(c1, "targetReconnect")

	// Disconnect first client
	h.RemoveClient(c1)

	// Connect second client before grace period expires
	time.Sleep(10 * time.Millisecond)
	c2 := NewClient(h, nil)
	h.AddClient(c2)
	h.Subscribe(c2, "targetReconnect")

	// Wait past the original grace period duration
	time.Sleep(30 * time.Millisecond)

	// Verify stopPoller was not called because c2 reconnected
	stopMu.Lock()
	if len(stoppedTargets) != 0 {
		t.Errorf("stopPoller was called despite client reconnection: %v", stoppedTargets)
	}
	stopMu.Unlock()

	// Now remove c2
	h.RemoveClient(c2)

	// Wait for grace period to expire
	time.Sleep(45 * time.Millisecond)

	stopMu.Lock()
	if len(stoppedTargets) != 1 || stoppedTargets[0] != "targetreconnect" {
		t.Errorf("expected stopPoller to be called after second client disconnects, got %v", stoppedTargets)
	}
	stopMu.Unlock()
}
