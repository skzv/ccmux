package telegram

import (
	"context"
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"strings"
	"testing"
)

// fakeDoer is an httpDoer that runs a function per request, so a test
// can assert on the outgoing request and craft the response.
type fakeDoer struct {
	fn func(*http.Request) (*http.Response, error)
}

func (f fakeDoer) Do(r *http.Request) (*http.Response, error) { return f.fn(r) }

// jsonResp builds an http.Response with a JSON body and status.
func jsonResp(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     http.Header{"Content-Type": []string{"application/json"}},
	}
}

func testClient(t *testing.T, fn func(*http.Request) (*http.Response, error)) *Client {
	t.Helper()
	return NewClient("SECRETTOKEN", WithHTTPClient(fakeDoer{fn: fn}))
}

func TestGetMe_Success(t *testing.T) {
	var gotPath string
	c := testClient(t, func(r *http.Request) (*http.Response, error) {
		gotPath = r.URL.Path
		return jsonResp(200, `{"ok":true,"result":{"id":42,"is_bot":true,"username":"ccmuxbot"}}`), nil
	})
	u, err := c.GetMe(context.Background())
	if err != nil {
		t.Fatalf("GetMe: %v", err)
	}
	if u.ID != 42 || u.Username != "ccmuxbot" || !u.IsBot {
		t.Errorf("unexpected user: %+v", u)
	}
	if !strings.Contains(gotPath, "/botSECRETTOKEN/getMe") {
		t.Errorf("path = %q, want it to contain the method", gotPath)
	}
}

func TestGetUpdates_Success(t *testing.T) {
	var body map[string]any
	c := testClient(t, func(r *http.Request) (*http.Response, error) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		return jsonResp(200, `{"ok":true,"result":[
			{"update_id":10,"message":{"message_id":1,"chat":{"id":7,"type":"private"},"text":"hi"}},
			{"update_id":11,"callback_query":{"id":"cb","from":{"id":7},"data":"approve|local:build|c1"}}
		]}`), nil
	})
	ups, err := c.GetUpdates(context.Background(), 10, 50)
	if err != nil {
		t.Fatalf("GetUpdates: %v", err)
	}
	if len(ups) != 2 {
		t.Fatalf("got %d updates, want 2", len(ups))
	}
	if ups[0].Message == nil || ups[0].Message.Text != "hi" {
		t.Errorf("update 0 message wrong: %+v", ups[0])
	}
	if ups[1].CallbackQuery == nil || ups[1].CallbackQuery.Data != "approve|local:build|c1" {
		t.Errorf("update 1 callback wrong: %+v", ups[1])
	}
	// Long-poll params are passed through.
	if body["offset"].(float64) != 10 || body["timeout"].(float64) != 50 {
		t.Errorf("offset/timeout not sent: %+v", body)
	}
}

func TestUpdate_ChatID(t *testing.T) {
	msg := Update{Message: &Message{Chat: Chat{ID: 5}}}
	if id, ok := msg.ChatID(); !ok || id != 5 {
		t.Errorf("message chat id = %d,%v", id, ok)
	}
	cb := Update{CallbackQuery: &CallbackQuery{From: User{ID: 9}, Message: &Message{Chat: Chat{ID: 8}}}}
	if id, ok := cb.ChatID(); !ok || id != 8 {
		t.Errorf("callback (with msg) chat id = %d,%v, want 8", id, ok)
	}
	iq := Update{InlineQuery: &InlineQuery{From: User{ID: 3}}}
	if id, ok := iq.ChatID(); !ok || id != 3 {
		t.Errorf("inline chat id = %d,%v, want 3", id, ok)
	}
	if _, ok := (Update{}).ChatID(); ok {
		t.Errorf("empty update should report no chat")
	}
}

func TestSendMessage_Success(t *testing.T) {
	var body SendMessageRequest
	c := testClient(t, func(r *http.Request) (*http.Response, error) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		return jsonResp(200, `{"ok":true,"result":{"message_id":99,"chat":{"id":7,"type":"private"}}}`), nil
	})
	m, err := c.SendMessage(context.Background(), SendMessageRequest{
		ChatID: 7, Text: "build needs you",
		ReplyMarkup: &InlineKeyboardMarkup{InlineKeyboard: [][]InlineKeyboardButton{{
			{Text: "Approve", CallbackData: "approve|local:build|c1"},
		}}},
	})
	if err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	if m.MessageID != 99 {
		t.Errorf("message id = %d, want 99", m.MessageID)
	}
	if body.ChatID != 7 || body.Text != "build needs you" {
		t.Errorf("request body wrong: %+v", body)
	}
	if body.ReplyMarkup == nil || body.ReplyMarkup.InlineKeyboard[0][0].CallbackData != "approve|local:build|c1" {
		t.Errorf("inline keyboard not serialized: %+v", body.ReplyMarkup)
	}
}

func TestSendDocument_Multipart(t *testing.T) {
	var (
		ct       string
		sawField bool
	)
	c := testClient(t, func(r *http.Request) (*http.Response, error) {
		ct = r.Header.Get("Content-Type")
		raw, _ := io.ReadAll(r.Body)
		sawField = strings.Contains(string(raw), "VISION") && strings.Contains(string(raw), "vision.md")
		return jsonResp(200, `{"ok":true,"result":{"message_id":5,"chat":{"id":7,"type":"private"}}}`), nil
	})
	_, err := c.SendDocument(context.Background(), 7, "vision.md", []byte("# VISION\nbody"), "vision.md")
	if err != nil {
		t.Fatalf("SendDocument: %v", err)
	}
	mt, _, _ := mime.ParseMediaType(ct)
	if mt != "multipart/form-data" {
		t.Errorf("content-type = %q, want multipart/form-data", ct)
	}
	if !sawField {
		t.Errorf("multipart body missing filename/content")
	}
}

func TestUnauthorized(t *testing.T) {
	c := testClient(t, func(r *http.Request) (*http.Response, error) {
		return jsonResp(401, `{"ok":false,"error_code":401,"description":"Unauthorized"}`), nil
	})
	_, err := c.GetMe(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
	if !IsUnauthorized(err) {
		t.Errorf("IsUnauthorized = false for %v", err)
	}
	if IsConflict(err) {
		t.Errorf("IsConflict should be false for a 401")
	}
}

func TestConflict(t *testing.T) {
	c := testClient(t, func(r *http.Request) (*http.Response, error) {
		return jsonResp(409, `{"ok":false,"error_code":409,"description":"Conflict: terminated by other getUpdates request"}`), nil
	})
	_, err := c.GetUpdates(context.Background(), 0, 50)
	if !IsConflict(err) {
		t.Errorf("IsConflict = false for %v", err)
	}
}

func TestNonJSON5xx(t *testing.T) {
	c := testClient(t, func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: 502,
			Body:       io.NopCloser(strings.NewReader("<html>bad gateway</html>")),
			Header:     http.Header{},
		}, nil
	})
	_, err := c.GetMe(context.Background())
	var apiErr *APIError
	if !asAPIError(err, &apiErr) {
		t.Fatalf("want *APIError, got %T (%v)", err, err)
	}
	if apiErr.Code != 502 {
		t.Errorf("code = %d, want 502", apiErr.Code)
	}
}

// TestErrorNeverLeaksToken is the load-bearing secrecy guarantee: no
// error path may surface the bot token.
func TestErrorNeverLeaksToken(t *testing.T) {
	// API-error path.
	c := testClient(t, func(r *http.Request) (*http.Response, error) {
		return jsonResp(401, `{"ok":false,"error_code":401,"description":"Unauthorized"}`), nil
	})
	_, err := c.GetMe(context.Background())
	if err != nil && strings.Contains(err.Error(), "SECRETTOKEN") {
		t.Errorf("API error leaked token: %v", err)
	}

	// Transport-error path where the transport embeds the URL (and thus
	// the token) in its error — scrub() must redact it.
	c2 := testClient(t, func(r *http.Request) (*http.Response, error) {
		return nil, &urlishError{msg: "dial tcp api.telegram.org: connect to https://api.telegram.org/botSECRETTOKEN/getMe failed"}
	})
	_, err2 := c2.GetMe(context.Background())
	if err2 == nil || strings.Contains(err2.Error(), "SECRETTOKEN") {
		t.Errorf("transport error leaked token: %v", err2)
	}
}

type urlishError struct{ msg string }

func (e *urlishError) Error() string { return e.msg }
