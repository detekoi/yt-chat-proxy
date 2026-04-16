package youtube

type InitialState struct {
	APIKey       string
	Continuation string
}

type LiveChatResponse struct {
	ContinuationContents struct {
		LiveChatContinuation struct {
			Continuations []struct {
				TimedContinuationData *struct {
					TimeoutMs    int    `json:"timeoutMs"`
					Continuation string `json:"continuation"`
				} `json:"timedContinuationData,omitempty"`
				InvalidationContinuationData *struct {
					TimeoutMs    int    `json:"timeoutMs"`
					Continuation string `json:"continuation"`
				} `json:"invalidationContinuationData,omitempty"`
			} `json:"continuations"`
			Actions []Action `json:"actions"`
		} `json:"liveChatContinuation"`
	} `json:"continuationContents"`
}

type Action struct {
	AddChatItemAction *AddChatItemAction `json:"addChatItemAction,omitempty"`
}

type AddChatItemAction struct {
	Item struct {
		LiveChatTextMessageRenderer    *LiveChatRenderer `json:"liveChatTextMessageRenderer,omitempty"`
		LiveChatPaidMessageRenderer    *LiveChatRenderer `json:"liveChatPaidMessageRenderer,omitempty"`
		LiveChatPaidStickerRenderer    *LiveChatRenderer `json:"liveChatPaidStickerRenderer,omitempty"`
		LiveChatMembershipItemRenderer *LiveChatRenderer `json:"liveChatMembershipItemRenderer,omitempty"`
	} `json:"item"`
}

type LiveChatRenderer struct {
	Id      string `json:"id"`
	Message struct {
		Runs []Run `json:"runs"`
	} `json:"message"`
	AuthorName struct {
		SimpleText string `json:"simpleText"`
	} `json:"authorName"`
	HeaderSubtext struct {
		SimpleText string `json:"simpleText"`
	} `json:"headerSubtext"`
	AuthorExternalChannelId string `json:"authorExternalChannelId"`
	AuthorPhoto             struct {
		Thumbnails []Thumbnail `json:"thumbnails"`
	} `json:"authorPhoto"`
	AuthorBadges []struct {
		LiveChatAuthorBadgeRenderer struct {
			Icon *struct {
				IconType string `json:"iconType"`
			} `json:"icon,omitempty"`
		} `json:"liveChatAuthorBadgeRenderer"`
	} `json:"authorBadges"`
	PurchaseAmountText struct {
		SimpleText string `json:"simpleText"`
	} `json:"purchaseAmountText"`
	BodyBackgroundColor   int64 `json:"bodyBackgroundColor"`
	HeaderBackgroundColor int64 `json:"headerBackgroundColor"`
}

type Run struct {
	Text  string `json:"text,omitempty"`
	Emoji *struct {
		EmojiId string `json:"emojiId"`
		Image   struct {
			Thumbnails []Thumbnail `json:"thumbnails"`
		} `json:"image"`
	} `json:"emoji,omitempty"`
}

type Thumbnail struct {
	Url    string `json:"url"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
}
