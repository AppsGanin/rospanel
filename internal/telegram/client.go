// Package telegram is a self-contained Telegram Bot API client plus the panel's
// admin bot: it long-polls for commands (view/add/remove users) and pushes
// scheduled backups to the linked admin chat. It deliberately depends only on the
// standard library — the Bot API is plain HTTP+JSON — so the panel keeps its
// minimal dependency surface.
package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// apiBase is the Telegram Bot API root; the token is appended per request.
const apiBase = "https://api.telegram.org/bot"

// Client talks to one bot (identified by its token). It's cheap to construct and
// safe for use from the single bot loop plus the occasional handler-driven send.
type Client struct {
	token string
	http  *http.Client
}

// NewClient builds a client for the given bot token. The HTTP timeout comfortably
// exceeds the long-poll window so GetUpdates isn't cut off mid-wait.
func NewClient(token string) *Client {
	return &Client{token: token, http: &http.Client{Timeout: 60 * time.Second}}
}

// Update is one polled event: a message or a callback-query (inline button tap).
type Update struct {
	UpdateID int64          `json:"update_id"`
	Message  *Message       `json:"message"`
	Callback *CallbackQuery `json:"callback_query"`
	// MyChatMember fires when the bot itself is added to, promoted in, or removed
	// from a chat. It is how the support bot learns a group's id without anyone
	// having to look one up by hand. Delivered only when asked for explicitly.
	MyChatMember *ChatMemberUpdated `json:"my_chat_member"`
}

// ChatMemberUpdated is a change to somebody's membership of a chat.
type ChatMemberUpdated struct {
	Chat          Chat       `json:"chat"`
	NewChatMember ChatMember `json:"new_chat_member"`
}

// InChat reports whether the subject is still a member after this change.
func (u *ChatMemberUpdated) InChat() bool {
	switch u.NewChatMember.Status {
	case "creator", "administrator", "member", "restricted":
		return true
	default: // "left", "kicked"
		return false
	}
}

// IsAdmin reports whether the subject can act as an administrator.
func (u *ChatMemberUpdated) IsAdmin() bool {
	return u.NewChatMember.Status == "creator" || u.NewChatMember.Status == "administrator"
}

// Message is the subset of a Telegram message the bot acts on. MessageThreadID is
// the forum topic a group message belongs to — the support relay keys on it to tell
// which user's thread an admin is answering in.
type Message struct {
	MessageID       int64       `json:"message_id"`
	From            *User       `json:"from"`
	Chat            Chat        `json:"chat"`
	Text            string      `json:"text"`
	MessageThreadID int64       `json:"message_thread_id"`
	Caption         string      `json:"caption"`
	Photo           []PhotoSize `json:"photo"`
	Document        *Document   `json:"document"`

	// Forum service messages. Telegram emits one of these when an admin creates,
	// renames, closes or reopens a topic; they carry a real sender and the topic's
	// thread id, so without them housekeeping looks exactly like a reply to relay.
	ForumTopicCreated  *struct{} `json:"forum_topic_created"`
	ForumTopicEdited   *struct{} `json:"forum_topic_edited"`
	ForumTopicClosed   *struct{} `json:"forum_topic_closed"`
	ForumTopicReopened *struct{} `json:"forum_topic_reopened"`
}

// IsForumService reports whether this is topic housekeeping rather than something a
// person wrote.
func (m *Message) IsForumService() bool {
	return m != nil && (m.ForumTopicCreated != nil || m.ForumTopicEdited != nil ||
		m.ForumTopicClosed != nil || m.ForumTopicReopened != nil)
}

// Body is the text a message carries, wherever it lives — plain text for a text
// message, the caption for one carrying media.
func (m *Message) Body() string {
	if m == nil {
		return ""
	}
	if m.Text != "" {
		return m.Text
	}
	return m.Caption
}

// PhotoSize / Document carry the file_id Telegram assigns to an uploaded file.
// Re-sending by that id costs no upload, which is the difference between one
// transfer and one per recipient when the same image goes out to a whole audience.
type PhotoSize struct {
	FileID string `json:"file_id"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
}
type Document struct {
	FileID string `json:"file_id"`
}

// MediaFileID returns the id of the file this message carries, or "" when it has
// none. For photos Telegram sends every rendition it made; the largest is the one
// worth re-sending, and the order it arrives in is documented only loosely, so the
// biggest is picked rather than the last.
func (m *Message) MediaFileID() string {
	if m == nil {
		return ""
	}
	if m.Document != nil && m.Document.FileID != "" {
		return m.Document.FileID
	}
	best, bestPx := "", 0
	for _, p := range m.Photo {
		if px := p.Width * p.Height; px >= bestPx {
			best, bestPx = p.FileID, px
		}
	}
	return best
}

// CallbackQuery is an inline-button tap (used for the delete confirmation).
type CallbackQuery struct {
	ID      string   `json:"id"`
	From    *User    `json:"from"`
	Message *Message `json:"message"`
	Data    string   `json:"data"`
}

// User / Chat carry only the identifiers the bot needs.
type User struct {
	ID        int64  `json:"id"`
	Username  string `json:"username"`
	FirstName string `json:"first_name"`
	// LangCode is the IETF tag of the client's interface language. Nothing reads it
	// yet — the panel has no i18n — but it is recorded per subscriber so the data
	// exists if broadcasts ever need to be segmented by language.
	LangCode string `json:"language_code"`
}
type Chat struct {
	ID      int64  `json:"id"`
	Type    string `json:"type"`     // "private" | "group" | "supergroup" | "channel"
	Title   string `json:"title"`    // group name (empty for private chats)
	IsForum bool   `json:"is_forum"` // supergroup with Topics enabled
}

// ChatMember is the subset of a member record the support-group check reads: the
// bot must be an administrator with CanManageTopics to open a thread per user.
type ChatMember struct {
	Status          string `json:"status"` // "creator" | "administrator" | "member" | ...
	CanManageTopics bool   `json:"can_manage_topics"`
}

// apiResponse is the envelope every Bot API method returns.
type apiResponse struct {
	OK          bool            `json:"ok"`
	Result      json.RawMessage `json:"result"`
	Description string          `json:"description"`
	ErrorCode   int             `json:"error_code"`
	Parameters  struct {
		RetryAfter int `json:"retry_after"` // seconds to wait, set on 429
	} `json:"parameters"`
}

// APIError is a non-OK Bot API reply. RetryAfter is the cool-off Telegram asks
// for on a 429; callers use it to back off for exactly as long as told rather
// than guessing.
type APIError struct {
	Code        int
	Description string
	RetryAfter  time.Duration
}

func (e *APIError) Error() string {
	return fmt.Sprintf("telegram api %d: %s", e.Code, e.Description)
}

// RetryAfter reports the cool-off Telegram requested, if err is a 429.
func RetryAfter(err error) (time.Duration, bool) {
	var ae *APIError
	if errors.As(err, &ae) && ae.RetryAfter > 0 {
		return ae.RetryAfter, true
	}
	return 0, false
}

// call POSTs a JSON method and unmarshals result into out (out may be nil).
func (c *Client) call(ctx context.Context, method string, payload any, out any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiBase+c.token+"/"+method, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	return c.do(req, out)
}

// send is call for the chat-directed methods: it waits for chatID's rate-limit
// slot first, and if Telegram still answers 429 it honours the retry_after it
// asked for and tries once more. One retry is enough — the wait it names clears
// the burst, and a second failure means something other than pacing is wrong.
func (c *Client) send(ctx context.Context, method string, chatID int64, payload any) error {
	return c.sendResult(ctx, method, chatID, payload, nil)
}

// sendResult is send for the calls whose reply the caller needs (out may be nil).
func (c *Client) sendResult(ctx context.Context, method string, chatID int64, payload, out any) error {
	if err := waitSlot(ctx, chatID); err != nil {
		return err
	}
	err := c.call(ctx, method, payload, out)
	d, throttled := RetryAfter(err)
	if !throttled {
		return err
	}
	backOff(chatID, d)
	if werr := waitSlot(ctx, chatID); werr != nil {
		return err // ctx ended mid-cooloff: report the 429, not the cancellation
	}
	return c.call(ctx, method, payload, out)
}

// do executes req, decodes the envelope, and surfaces a non-OK API error.
func (c *Client) do(req *http.Request, out any) error {
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	var env apiResponse
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		return err
	}
	if !env.OK {
		return &APIError{
			Code:        env.ErrorCode,
			Description: env.Description,
			RetryAfter:  time.Duration(env.Parameters.RetryAfter) * time.Second,
		}
	}
	if out != nil && len(env.Result) > 0 {
		return json.Unmarshal(env.Result, out)
	}
	return nil
}

// defaultAllowedUpdates is what a bot receives unless it asks for more. Telegram
// withholds my_chat_member unless it is listed, so a bot that doesn't need to know
// about its own group memberships isn't billed an update for them.
var defaultAllowedUpdates = []string{"message", "callback_query"}

// GetUpdates long-polls for messages and callback queries past offset.
func (c *Client) GetUpdates(ctx context.Context, offset int64, timeout int) ([]Update, error) {
	return c.GetUpdatesFor(ctx, offset, timeout, defaultAllowedUpdates)
}

// GetUpdatesFor is GetUpdates for a bot that needs a different update set.
func (c *Client) GetUpdatesFor(ctx context.Context, offset int64, timeout int, allowed []string) ([]Update, error) {
	kinds, err := json.Marshal(allowed)
	if err != nil {
		return nil, err
	}
	q := url.Values{}
	q.Set("offset", strconv.FormatInt(offset, 10))
	q.Set("timeout", strconv.Itoa(timeout))
	q.Set("allowed_updates", string(kinds))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		apiBase+c.token+"/getUpdates?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	var updates []Update
	if err := c.do(req, &updates); err != nil {
		return nil, err
	}
	return updates, nil
}

// GetMe returns the bot's own account (used to show its @username in the panel).
func (c *Client) GetMe(ctx context.Context) (*User, error) {
	var u User
	if err := c.call(ctx, "getMe", map[string]any{}, &u); err != nil {
		return nil, err
	}
	return &u, nil
}

// InlineButton is one inline-keyboard button (label + callback payload).
type InlineButton struct {
	Text         string      `json:"text"`
	CallbackData string      `json:"callback_data,omitempty"`
	URL          string      `json:"url,omitempty"`     // URL button (mutually exclusive with callback_data)
	WebApp       *WebAppInfo `json:"web_app,omitempty"` // opens a Mini App in-place (private chats only)
}

// WebAppInfo points a web_app button at an HTTPS page opened inside Telegram as a
// Mini App. Inline web_app buttons need no @BotFather domain setup, but work only
// in private chats and require an https:// URL.
type WebAppInfo struct {
	URL string `json:"url"`
}

// SendMessage sends a plain HTML-formatted message (no buttons). HTML parse mode
// lets the bot bold headers; all dynamic text must be escaped by the caller (esc).
func (c *Client) SendMessage(ctx context.Context, chatID int64, html string) error {
	return c.SendMenu(ctx, chatID, html, nil)
}

// SendMenu sends an HTML message with an inline keyboard (rows of buttons). Pass a
// nil/empty rows for a plain message.
func (c *Client) SendMenu(ctx context.Context, chatID int64, html string, rows [][]InlineButton) error {
	payload := map[string]any{
		"chat_id":                  chatID,
		"text":                     html,
		"parse_mode":               "HTML",
		"disable_web_page_preview": true,
	}
	if len(rows) > 0 {
		payload["reply_markup"] = map[string]any{"inline_keyboard": rows}
	}
	return c.send(ctx, "sendMessage", chatID, payload)
}

// EditMenu replaces the text + inline keyboard of an existing message, so the bot's
// menus navigate in place instead of stacking new messages.
func (c *Client) EditMenu(ctx context.Context, chatID, messageID int64, html string, rows [][]InlineButton) error {
	payload := map[string]any{
		"chat_id":                  chatID,
		"message_id":               messageID,
		"text":                     html,
		"parse_mode":               "HTML",
		"disable_web_page_preview": true,
	}
	if len(rows) > 0 {
		payload["reply_markup"] = map[string]any{"inline_keyboard": rows}
	}
	return c.send(ctx, "editMessageText", chatID, payload)
}

// AnswerCallback acknowledges a button tap so Telegram stops the spinner.
func (c *Client) AnswerCallback(ctx context.Context, id, text string) error {
	return c.call(ctx, "answerCallbackQuery", map[string]any{
		"callback_query_id": id,
		"text":              text,
	}, nil)
}

// Forum / relay methods. These back the support bot: it opens one topic per user in
// the operator's supergroup, forwards what the user writes into that topic, and
// copies the admin's reply back. Relaying works on message IDs alone, so any media
// type passes through without the bot parsing it.

// CreateForumTopic opens a new topic in a forum supergroup and returns its
// message_thread_id.
func (c *Client) CreateForumTopic(ctx context.Context, chatID int64, name string) (int64, error) {
	var topic struct {
		MessageThreadID int64 `json:"message_thread_id"`
	}
	if err := c.sendResult(ctx, "createForumTopic", chatID, map[string]any{
		"chat_id": chatID,
		"name":    name,
	}, &topic); err != nil {
		return 0, err
	}
	return topic.MessageThreadID, nil
}

// ReopenForumTopic re-opens a closed topic. Admins close a thread as the natural
// "handled" gesture, and the relay keeps one thread per user forever — without a
// reopen, that user's support would be dead from then on.
func (c *Client) ReopenForumTopic(ctx context.Context, chatID, threadID int64) error {
	return c.call(ctx, "reopenForumTopic", map[string]any{
		"chat_id":           chatID,
		"message_thread_id": threadID,
	}, nil)
}

// ForwardMessage forwards a message into a chat, optionally into a forum topic
// (threadID 0 = the General thread). Forwarding keeps the "from" attribution.
func (c *Client) ForwardMessage(ctx context.Context, toChatID, threadID, fromChatID, messageID int64) error {
	payload := map[string]any{
		"chat_id":      toChatID,
		"from_chat_id": fromChatID,
		"message_id":   messageID,
	}
	if threadID != 0 {
		payload["message_thread_id"] = threadID
	}
	return c.send(ctx, "forwardMessage", toChatID, payload)
}

// CopyMessage sends a copy of a message with no "forwarded from" header — used for
// the admin's reply, so the user sees it as coming from the bot rather than from a
// group they can't see.
func (c *Client) CopyMessage(ctx context.Context, toChatID, fromChatID, messageID int64) error {
	return c.send(ctx, "copyMessage", toChatID, map[string]any{
		"chat_id":      toChatID,
		"from_chat_id": fromChatID,
		"message_id":   messageID,
	})
}

// SendTopic posts an HTML message into a forum topic and returns its message ID (so
// the caller can pin it). threadID 0 posts to the General thread.
func (c *Client) SendTopic(ctx context.Context, chatID, threadID int64, html string) (int64, error) {
	payload := map[string]any{
		"chat_id":                  chatID,
		"text":                     html,
		"parse_mode":               "HTML",
		"disable_web_page_preview": true,
	}
	if threadID != 0 {
		payload["message_thread_id"] = threadID
	}
	var m Message
	if err := c.sendResult(ctx, "sendMessage", chatID, payload, &m); err != nil {
		return 0, err
	}
	return m.MessageID, nil
}

// PinChatMessage pins a message (the user card at the top of their topic). Pinned
// silently — the admins are already notified by the forwarded message itself.
func (c *Client) PinChatMessage(ctx context.Context, chatID, messageID int64) error {
	return c.call(ctx, "pinChatMessage", map[string]any{
		"chat_id":              chatID,
		"message_id":           messageID,
		"disable_notification": true,
	}, nil)
}

// GetChat returns a chat's record — the support-group check reads Type and IsForum
// from it.
func (c *Client) GetChat(ctx context.Context, chatID int64) (*Chat, error) {
	var ch Chat
	if err := c.call(ctx, "getChat", map[string]any{"chat_id": chatID}, &ch); err != nil {
		return nil, err
	}
	return &ch, nil
}

// GetChatMember returns one member's record — used to confirm the bot itself is an
// administrator of the support group.
func (c *Client) GetChatMember(ctx context.Context, chatID, userID int64) (*ChatMember, error) {
	var m ChatMember
	if err := c.call(ctx, "getChatMember", map[string]any{
		"chat_id": chatID,
		"user_id": userID,
	}, &m); err != nil {
		return nil, err
	}
	return &m, nil
}

// SendDocument uploads a file to a chat as a document, with an optional caption.
func (c *Client) SendDocument(ctx context.Context, chatID int64, filename, caption string, r io.Reader) error {
	_, err := c.upload(ctx, "sendDocument", "document", chatID, filename, caption, nil, r)
	return err
}

// SendPhoto uploads an image to a chat (shown inline), with an optional HTML
// caption. Used to deliver the subscription QR code.
func (c *Client) SendPhoto(ctx context.Context, chatID int64, filename, caption string, r io.Reader) error {
	_, err := c.upload(ctx, "sendPhoto", "photo", chatID, filename, caption, nil, r)
	return err
}

// UploadPhoto / UploadDocument send a file and return the file_id Telegram assigned
// it, so the same file can be re-sent to everyone else with SendPhotoID /
// SendDocumentID instead of being uploaded again per recipient.
func (c *Client) UploadPhoto(ctx context.Context, chatID int64, filename, caption string, rows [][]InlineButton, r io.Reader) (string, error) {
	m, err := c.upload(ctx, "sendPhoto", "photo", chatID, filename, caption, rows, r)
	if err != nil {
		return "", err
	}
	return m.MediaFileID(), nil
}

func (c *Client) UploadDocument(ctx context.Context, chatID int64, filename, caption string, rows [][]InlineButton, r io.Reader) (string, error) {
	m, err := c.upload(ctx, "sendDocument", "document", chatID, filename, caption, rows, r)
	if err != nil {
		return "", err
	}
	return m.MediaFileID(), nil
}

// SendPhotoID / SendDocumentID send an already-uploaded file by its file_id, with an
// optional HTML caption and inline keyboard.
func (c *Client) SendPhotoID(ctx context.Context, chatID int64, fileID, caption string, rows [][]InlineButton) error {
	return c.sendMedia(ctx, "sendPhoto", "photo", chatID, fileID, caption, rows)
}

func (c *Client) SendDocumentID(ctx context.Context, chatID int64, fileID, caption string, rows [][]InlineButton) error {
	return c.sendMedia(ctx, "sendDocument", "document", chatID, fileID, caption, rows)
}

func (c *Client) sendMedia(ctx context.Context, method, field string, chatID int64, fileID, caption string, rows [][]InlineButton) error {
	payload := map[string]any{
		"chat_id": chatID,
		field:     fileID,
	}
	if caption != "" {
		payload["caption"] = caption
		payload["parse_mode"] = "HTML"
	}
	if len(rows) > 0 {
		payload["reply_markup"] = map[string]any{"inline_keyboard": rows}
	}
	return c.send(ctx, method, chatID, payload)
}

// BotCommand is one entry of the bot's command menu.
type BotCommand struct {
	Command     string `json:"command"`     // without the leading slash
	Description string `json:"description"` // shown next to it in the menu
}

// SetMyCommands publishes the bot's command menu. Without it a command nobody was
// told about is a command nobody uses — which matters for an opt-out that has to be
// findable to count as one.
func (c *Client) SetMyCommands(ctx context.Context, cmds []BotCommand) error {
	return c.call(ctx, "setMyCommands", map[string]any{"commands": cmds}, nil)
}

// upload streams r as a multipart file for sendDocument/sendPhoto (field is
// "document" or "photo"). HTML parse mode is set so captions can be formatted.
// It returns the resulting message so callers can pick up the file_id Telegram
// assigned (see MediaFileID).
func (c *Client) upload(ctx context.Context, method, field string, chatID int64, filename, caption string, rows [][]InlineButton, r io.Reader) (*Message, error) {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("chat_id", strconv.FormatInt(chatID, 10))
	if caption != "" {
		_ = mw.WriteField("caption", caption)
		_ = mw.WriteField("parse_mode", "HTML")
	}
	// An uploaded message is a real delivery like any other: it carries the same
	// keyboard the by-file_id sends do, or the first recipient of a broadcast would
	// be the only one without buttons.
	if len(rows) > 0 {
		markup, err := json.Marshal(map[string]any{"inline_keyboard": rows})
		if err != nil {
			return nil, err
		}
		_ = mw.WriteField("reply_markup", string(markup))
	}
	fw, err := mw.CreateFormFile(field, filename)
	if err != nil {
		return nil, err
	}
	if _, err := io.Copy(fw, r); err != nil {
		return nil, err
	}
	if err := mw.Close(); err != nil {
		return nil, err
	}
	var sent Message
	// The body is fully buffered, so a throttled upload can be replayed verbatim
	// from the same bytes — same one-retry rule as send.
	post := func() error {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiBase+c.token+"/"+method, bytes.NewReader(buf.Bytes()))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", mw.FormDataContentType())
		return c.do(req, &sent)
	}
	if err := waitSlot(ctx, chatID); err != nil {
		return nil, err
	}
	err = post()
	d, throttled := RetryAfter(err)
	if !throttled {
		if err != nil {
			return nil, err
		}
		return &sent, nil
	}
	backOff(chatID, d)
	if werr := waitSlot(ctx, chatID); werr != nil {
		return nil, err
	}
	if err := post(); err != nil {
		return nil, err
	}
	return &sent, nil
}
