package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// defaultBaseURL is the Bot API root. Overridable in tests via
// WithBaseURL so the whole client exercises real HTTP against a fake
// transport without touching the network.
const defaultBaseURL = "https://api.telegram.org"

// maxRespBytes caps how much of a Bot API response we read, matching the
// daemon client's defensiveness against a hostile/buggy upstream. Note
// sticker/file results are never read through this client.
const maxRespBytes = 16 << 20

// httpDoer is the seam the client talks through. *http.Client satisfies
// it; tests inject a fake.
type httpDoer interface {
	Do(*http.Request) (*http.Response, error)
}

// Client is a minimal Telegram Bot API client. It is safe for
// concurrent use (the underlying http.Client is). The bot token lives
// only in the request URL; it is never placed in error messages, so a
// leaked error string can't leak the token.
type Client struct {
	token   string
	baseURL string
	http    httpDoer
}

// Option configures a Client.
type Option func(*Client)

// WithBaseURL overrides the Bot API root (tests).
func WithBaseURL(u string) Option { return func(c *Client) { c.baseURL = u } }

// WithHTTPClient injects the transport (tests, or a custom http.Client).
func WithHTTPClient(d httpDoer) Option { return func(c *Client) { c.http = d } }

// NewClient builds a Bot API client for the given token. The default
// transport has no overall timeout — long-poll getUpdates holds a
// request open for the poll interval, so callers control duration via
// the per-call context instead.
func NewClient(token string, opts ...Option) *Client {
	c := &Client{
		token:   token,
		baseURL: defaultBaseURL,
		http: &http.Client{
			Transport: &http.Transport{
				MaxIdleConns:        4,
				MaxIdleConnsPerHost: 2,
				IdleConnTimeout:     90 * time.Second,
			},
		},
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

// APIError is a structured Bot API failure. It deliberately omits the
// request URL (which contains the token); only the method name and the
// upstream-provided code/description are exposed.
type APIError struct {
	Method      string
	Code        int
	Description string
}

func (e *APIError) Error() string {
	if e.Description == "" {
		return fmt.Sprintf("telegram %s: HTTP %d", e.Method, e.Code)
	}
	return fmt.Sprintf("telegram %s: %d %s", e.Method, e.Code, e.Description)
}

// IsConflict reports a 409 — another process is already long-polling
// this token. One token is owned by exactly one daemon.
func IsConflict(err error) bool {
	var e *APIError
	return asAPIError(err, &e) && e.Code == 409
}

// IsUnauthorized reports a 401 — the token is wrong/revoked.
func IsUnauthorized(err error) bool {
	var e *APIError
	return asAPIError(err, &e) && e.Code == 401
}

func asAPIError(err error, target **APIError) bool {
	for err != nil {
		if e, ok := err.(*APIError); ok {
			*target = e
			return true
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}

func (c *Client) methodURL(method string) string {
	return c.baseURL + "/bot" + c.token + "/" + method
}

// callJSON POSTs a JSON payload (or nil) to a Bot API method and decodes
// the result envelope into out (which may be nil).
func (c *Client) callJSON(ctx context.Context, method string, payload, out any) error {
	var body io.Reader
	if payload != nil {
		b, err := json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("telegram %s: marshal request: %w", method, err)
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.methodURL(method), body)
	if err != nil {
		return fmt.Errorf("telegram %s: build request: %w", method, err)
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return c.do(req, method, out)
}

// do executes a prepared request and unpacks the Bot API envelope.
func (c *Client) do(req *http.Request, method string, out any) error {
	resp, err := c.http.Do(req)
	if err != nil {
		// Don't wrap with the URL — http errors can include it, and it
		// carries the token. Surface a token-free message instead.
		return fmt.Errorf("telegram %s: request failed: %w", method, scrub(err, c.token))
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(io.LimitReader(resp.Body, maxRespBytes))
	if err != nil {
		return fmt.Errorf("telegram %s: read response: %w", method, scrub(err, c.token))
	}

	var env struct {
		OK          bool            `json:"ok"`
		Result      json.RawMessage `json:"result"`
		ErrorCode   int             `json:"error_code"`
		Description string          `json:"description"`
	}
	if jsonErr := json.Unmarshal(data, &env); jsonErr != nil {
		// Non-JSON body (e.g. an HTML 5xx from a proxy). Surface the
		// HTTP status so retry/backoff logic upstream can act on it.
		return &APIError{Method: method, Code: resp.StatusCode, Description: "non-JSON response"}
	}
	if !env.OK {
		code := env.ErrorCode
		if code == 0 {
			code = resp.StatusCode
		}
		return &APIError{Method: method, Code: code, Description: env.Description}
	}
	if out != nil && len(env.Result) > 0 {
		if err := json.Unmarshal(env.Result, out); err != nil {
			return fmt.Errorf("telegram %s: decode result: %w", method, err)
		}
	}
	return nil
}

// scrub replaces the token in an error string with a redaction marker.
// Belt-and-suspenders: callers already avoid putting the URL in errors,
// but a transport may embed it in its own error.
func scrub(err error, token string) error {
	if err == nil || token == "" {
		return err
	}
	msg := err.Error()
	if !strings.Contains(msg, token) {
		return err
	}
	return fmt.Errorf("%s", strings.ReplaceAll(msg, token, "<token>"))
}

// GetMe validates the token and returns the bot's identity.
func (c *Client) GetMe(ctx context.Context) (*User, error) {
	var u User
	if err := c.callJSON(ctx, "getMe", nil, &u); err != nil {
		return nil, err
	}
	return &u, nil
}

// GetUpdates long-polls for the next batch of updates. offset is the
// last-acknowledged update id + 1; timeoutSecs is the server-side
// long-poll hold. The caller's ctx must outlast timeoutSecs.
func (c *Client) GetUpdates(ctx context.Context, offset int64, timeoutSecs int) ([]Update, error) {
	payload := map[string]any{
		"offset":  offset,
		"timeout": timeoutSecs,
		// Only ask for the update kinds we handle, so we don't ack-skip
		// types we didn't process.
		"allowed_updates": []string{"message", "callback_query", "inline_query"},
	}
	var updates []Update
	if err := c.callJSON(ctx, "getUpdates", payload, &updates); err != nil {
		return nil, err
	}
	return updates, nil
}

// SendMessage sends a text message and returns the sent message (its id
// is needed to edit it later).
func (c *Client) SendMessage(ctx context.Context, req SendMessageRequest) (*Message, error) {
	var m Message
	if err := c.callJSON(ctx, "sendMessage", req, &m); err != nil {
		return nil, err
	}
	return &m, nil
}

// EditMessageText rewrites an already-sent message in place.
func (c *Client) EditMessageText(ctx context.Context, req EditMessageTextRequest) error {
	return c.callJSON(ctx, "editMessageText", req, nil)
}

// AnswerCallbackQuery acknowledges a button tap. The Bot API shows a
// progress spinner on the button until this is called, so it must run
// even when no toast text is needed.
func (c *Client) AnswerCallbackQuery(ctx context.Context, id, text string, showAlert bool) error {
	payload := map[string]any{"callback_query_id": id}
	if text != "" {
		payload["text"] = text
	}
	if showAlert {
		payload["show_alert"] = true
	}
	return c.callJSON(ctx, "answerCallbackQuery", payload, nil)
}

// AnswerInlineQuery responds to an inline query with article results.
// cacheTimeSecs of 0 disables Telegram-side caching (right for results
// that depend on live session state).
func (c *Client) AnswerInlineQuery(ctx context.Context, id string, results []InlineQueryResultArticle, cacheTimeSecs int) error {
	payload := map[string]any{
		"inline_query_id": id,
		"results":         results,
		"cache_time":      cacheTimeSecs,
		"is_personal":     true,
	}
	return c.callJSON(ctx, "answerInlineQuery", payload, nil)
}

// SetMyCommands registers the composer "/" command menu.
func (c *Client) SetMyCommands(ctx context.Context, cmds []BotCommand) error {
	return c.callJSON(ctx, "setMyCommands", map[string]any{"commands": cmds}, nil)
}

// SendDocument uploads an in-memory file (e.g. a rendered .md note) as a
// document. The June-2026 in-app browser renders .md natively, so no
// server is needed for the user to read it formatted.
func (c *Client) SendDocument(ctx context.Context, chatID int64, filename string, content []byte, caption string) (*Message, error) {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	if err := mw.WriteField("chat_id", strconv.FormatInt(chatID, 10)); err != nil {
		return nil, fmt.Errorf("telegram sendDocument: %w", err)
	}
	if caption != "" {
		if err := mw.WriteField("caption", caption); err != nil {
			return nil, fmt.Errorf("telegram sendDocument: %w", err)
		}
	}
	fw, err := mw.CreateFormFile("document", filename)
	if err != nil {
		return nil, fmt.Errorf("telegram sendDocument: %w", err)
	}
	if _, err := fw.Write(content); err != nil {
		return nil, fmt.Errorf("telegram sendDocument: %w", err)
	}
	if err := mw.Close(); err != nil {
		return nil, fmt.Errorf("telegram sendDocument: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.methodURL("sendDocument"), &buf)
	if err != nil {
		return nil, fmt.Errorf("telegram sendDocument: %w", err)
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	var m Message
	if err := c.do(req, "sendDocument", &m); err != nil {
		return nil, err
	}
	return &m, nil
}
