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
}

// Message is the subset of a Telegram message the bot acts on.
type Message struct {
	MessageID int64  `json:"message_id"`
	From      *User  `json:"from"`
	Chat      Chat   `json:"chat"`
	Text      string `json:"text"`
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
}
type Chat struct {
	ID int64 `json:"id"`
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
	if err := waitSlot(ctx, chatID); err != nil {
		return err
	}
	err := c.call(ctx, method, payload, nil)
	d, throttled := RetryAfter(err)
	if !throttled {
		return err
	}
	backOff(chatID, d)
	if werr := waitSlot(ctx, chatID); werr != nil {
		return err // ctx ended mid-cooloff: report the 429, not the cancellation
	}
	return c.call(ctx, method, payload, nil)
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

// GetUpdates long-polls for new updates past offset, blocking up to timeout
// seconds. Only messages and callback queries are requested.
func (c *Client) GetUpdates(ctx context.Context, offset int64, timeout int) ([]Update, error) {
	q := url.Values{}
	q.Set("offset", strconv.FormatInt(offset, 10))
	q.Set("timeout", strconv.Itoa(timeout))
	q.Set("allowed_updates", `["message","callback_query"]`)
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

// SendDocument uploads a file to a chat as a document, with an optional caption.
func (c *Client) SendDocument(ctx context.Context, chatID int64, filename, caption string, r io.Reader) error {
	return c.upload(ctx, "sendDocument", "document", chatID, filename, caption, r)
}

// SendPhoto uploads an image to a chat (shown inline), with an optional HTML
// caption. Used to deliver the subscription QR code.
func (c *Client) SendPhoto(ctx context.Context, chatID int64, filename, caption string, r io.Reader) error {
	return c.upload(ctx, "sendPhoto", "photo", chatID, filename, caption, r)
}

// upload streams r as a multipart file for sendDocument/sendPhoto (field is
// "document" or "photo"). HTML parse mode is set so captions can be formatted.
func (c *Client) upload(ctx context.Context, method, field string, chatID int64, filename, caption string, r io.Reader) error {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("chat_id", strconv.FormatInt(chatID, 10))
	if caption != "" {
		_ = mw.WriteField("caption", caption)
		_ = mw.WriteField("parse_mode", "HTML")
	}
	fw, err := mw.CreateFormFile(field, filename)
	if err != nil {
		return err
	}
	if _, err := io.Copy(fw, r); err != nil {
		return err
	}
	if err := mw.Close(); err != nil {
		return err
	}
	// The body is fully buffered, so a throttled upload can be replayed verbatim
	// from the same bytes — same one-retry rule as send.
	post := func() error {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiBase+c.token+"/"+method, bytes.NewReader(buf.Bytes()))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", mw.FormDataContentType())
		return c.do(req, nil)
	}
	if err := waitSlot(ctx, chatID); err != nil {
		return err
	}
	err = post()
	d, throttled := RetryAfter(err)
	if !throttled {
		return err
	}
	backOff(chatID, d)
	if werr := waitSlot(ctx, chatID); werr != nil {
		return err
	}
	return post()
}
