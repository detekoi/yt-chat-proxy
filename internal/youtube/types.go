package youtube

type InitialState struct {
	APIKey       string
	Continuation string
	VideoId      string
}

type resolveUrlResponse struct {
	Endpoint struct {
		WatchEndpoint struct {
			VideoId string `json:"videoId"`
		} `json:"watchEndpoint"`
		BrowseEndpoint struct {
			BrowseId string `json:"browseId"`
			Params   string `json:"params"`
		} `json:"browseEndpoint"`
	} `json:"endpoint"`
}

type browseResponse struct {
	Contents struct {
		TwoColumnBrowseResultsRenderer struct {
			Tabs []struct {
				TabRenderer struct {
					Content struct {
						RichGridRenderer struct {
							Contents []struct {
								RichItemRenderer struct {
									Content struct {
										VideoRenderer struct {
											VideoId            string `json:"videoId"`
											ThumbnailOverlays []struct {
												ThumbnailOverlayTimeStatusRenderer struct {
													Style string `json:"style"`
												} `json:"thumbnailOverlayTimeStatusRenderer"`
											} `json:"thumbnailOverlays"`
										} `json:"videoRenderer"`
										// Newer channel pages render grid items as lockupViewModel
										// instead of videoRenderer; the LIVE marker moves to a
										// thumbnailBadgeViewModel with badgeStyle THUMBNAIL_OVERLAY_BADGE_STYLE_LIVE.
										LockupViewModel struct {
											ContentId    string `json:"contentId"`
											ContentType  string `json:"contentType"`
											ContentImage struct {
												ThumbnailViewModel struct {
													Overlays []struct {
														ThumbnailBottomOverlayViewModel struct {
															Badges []struct {
																ThumbnailBadgeViewModel struct {
																	Text       string `json:"text"`
																	BadgeStyle string `json:"badgeStyle"`
																} `json:"thumbnailBadgeViewModel"`
															} `json:"badges"`
														} `json:"thumbnailBottomOverlayViewModel"`
													} `json:"overlays"`
												} `json:"thumbnailViewModel"`
											} `json:"contentImage"`
										} `json:"lockupViewModel"`
									} `json:"content"`
								} `json:"richItemRenderer"`
							} `json:"contents"`
						} `json:"richGridRenderer"`
					} `json:"content"`
				} `json:"tabRenderer"`
			} `json:"tabs"`
		} `json:"twoColumnBrowseResultsRenderer"`
	} `json:"contents"`
}

type nextResponse struct {
	Contents struct {
		TwoColumnWatchNextResults struct {
			ConversationBar struct {
				LiveChatRenderer struct {
					// True when the video has ended and YouTube is offering a chat *replay*.
					// A replay continuation is not pollable via get_live_chat (HTTP 400).
					IsReplay      bool `json:"isReplay"`
					Continuations []struct {
						ReloadContinuationData struct {
							Continuation string `json:"continuation"`
						} `json:"reloadContinuationData"`
					} `json:"continuations"`
				} `json:"liveChatRenderer"`
			} `json:"conversationBar"`
		} `json:"twoColumnWatchNextResults"`
	} `json:"contents"`
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
	Text  string    `json:"text,omitempty"`
	Emoji *EmojiRun `json:"emoji,omitempty"`
}

type EmojiRun struct {
	EmojiId   string     `json:"emojiId"`
	Image     EmojiImage `json:"image"`
	Shortcuts []string   `json:"shortcuts,omitempty"`
}

type EmojiImage struct {
	Thumbnails    []Thumbnail        `json:"thumbnails"`
	Accessibility *EmojiAccessibility `json:"accessibility,omitempty"`
}

type EmojiAccessibility struct {
	AccessibilityData struct {
		Label string `json:"label"`
	} `json:"accessibilityData"`
}

type Thumbnail struct {
	Url    string `json:"url"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
}
