// Package client is a small client for the RedirHub public API (v1).
package client

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// ErrNotFound is returned when the API answers 404, so a resource deleted outside Terraform
// can be dropped from state instead of failing the plan.
var ErrNotFound = errors.New("not found")

const maxAttempts = 6

type Client struct {
	BaseURL   string
	Token     string
	Workspace string
	UserAgent string
	HTTP      *http.Client
	// RetryWait is the first wait before a retry; it doubles with every attempt.
	RetryWait time.Duration
}

func New(baseURL, token, workspace, userAgent string) *Client {
	return &Client{
		BaseURL:   strings.TrimRight(baseURL, "/"),
		Token:     token,
		Workspace: workspace,
		UserAgent: userAgent,
		HTTP:      &http.Client{Timeout: 60 * time.Second},
		RetryWait: time.Second,
	}
}

// APIError carries the API's message and field errors, e.g. a plan limit.
type APIError struct {
	Status  int
	Message string              `json:"message"`
	Errors  map[string][]string `json:"errors"`
}

func (e *APIError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "RedirHub API returned %d", e.Status)
	if e.Message != "" {
		fmt.Fprintf(&b, ": %s", e.Message)
	}
	for field, msgs := range e.Errors {
		for _, m := range msgs {
			fmt.Fprintf(&b, "\n  %s: %s", field, m)
		}
	}
	return b.String()
}

// do sends one API call and retries it on a rate limit, a server error or a dropped
// connection. Every POST carries one Idempotency-Key for all its attempts, so a retry of a
// create the API already handled gets the first response back instead of a second record.
// Single items come back as the object itself; lists and created links under `data`.
func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	var payload []byte
	if body != nil {
		var err error
		if payload, err = json.Marshal(body); err != nil {
			return err
		}
	}
	idempotencyKey := ""
	if method == http.MethodPost {
		idempotencyKey = newKey()
	}

	var lastErr error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		if attempt > 0 {
			if err := c.sleep(ctx, attempt, lastErr); err != nil {
				return err
			}
		}

		req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, bytes.NewReader(payload))
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+c.Token)
		req.Header.Set("Accept", "application/json")
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("User-Agent", c.UserAgent)
		if c.Workspace != "" {
			req.Header.Set("Workspace", c.Workspace)
		}
		if idempotencyKey != "" {
			req.Header.Set("Idempotency-Key", idempotencyKey)
		}

		resp, err := c.HTTP.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			lastErr = err
			continue
		}
		data, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		switch {
		case resp.StatusCode == http.StatusNotFound:
			return ErrNotFound
		case resp.StatusCode == http.StatusTooManyRequests,
			resp.StatusCode == http.StatusConflict && idempotencyKey != "", // the first attempt is still running
			resp.StatusCode >= 500:
			lastErr = &retryAfter{apiError(resp.StatusCode, data), resp.Header.Get("Retry-After")}
			continue
		case resp.StatusCode >= 300:
			return apiError(resp.StatusCode, data)
		}
		if out != nil && len(data) > 0 {
			return json.Unmarshal(data, out)
		}
		return nil
	}
	var wait *retryAfter
	if errors.As(lastErr, &wait) {
		return wait.err
	}
	return lastErr
}

type retryAfter struct {
	err   error
	after string
}

func (r *retryAfter) Error() string { return r.err.Error() }

// sleep waits before the next attempt: as long as the API asks, or a doubling backoff.
func (c *Client) sleep(ctx context.Context, attempt int, lastErr error) error {
	wait := c.RetryWait << (attempt - 1)
	var r *retryAfter
	if errors.As(lastErr, &r) && r.after != "" {
		if s, err := strconv.Atoi(r.after); err == nil {
			wait = time.Duration(s) * time.Second
		}
	}
	select {
	case <-time.After(wait):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func apiError(status int, data []byte) *APIError {
	e := &APIError{Status: status}
	if json.Unmarshal(data, e) != nil || e.Message == "" {
		e.Message = strings.TrimSpace(string(data))
	}
	return e
}

func newKey() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// UTM holds a link's UTM parameters.
type UTM struct {
	Source   string `json:"source,omitempty"`
	Medium   string `json:"medium,omitempty"`
	Campaign string `json:"campaign,omitempty"`
	Term     string `json:"term,omitempty"`
	Content  string `json:"content,omitempty"`
}

// QR is the style of a short link's QR code.
type QR struct {
	Theme   string `json:"theme,omitempty"`
	Color   string `json:"color,omitempty"`
	Logo    *bool  `json:"logo,omitempty"`
	ShowURL *bool  `json:"show_url,omitempty"`
	Margin  string `json:"margin,omitempty"`
}

// Link is a record of `/links`, whatever its handler.
type Link struct {
	ID                 string   `json:"id"`
	Handler            string   `json:"handler"`
	Host               string   `json:"host"`
	Alias              *string  `json:"alias"`
	URL                string   `json:"url"`
	HTTPS              bool     `json:"https"`
	Destinations       []string `json:"destinations"`
	DestinationRouting *string  `json:"destination_routing"`
	Type               *string  `json:"type"`
	Title              *string  `json:"title"`
	Description        *string  `json:"description"`
	Status             string   `json:"status"`
	PausedBy           *string  `json:"paused_by"`
	ForwardPath        *bool    `json:"forward_path"`
	ForwardQuery       *bool    `json:"forward_query"`
	UTM                *UTM     `json:"utm"`
	QR                 *QR      `json:"qr"`
	Tags               []string `json:"tags"`
	HasPassword        bool     `json:"has_password"`
}

// Paused is true only for a link a person paused; a plan or visits pause lifts on its own.
func (l *Link) Paused() bool {
	return l.Status == "paused" && l.PausedBy != nil && *l.PausedBy == "user"
}

func (c *Client) CreateLink(ctx context.Context, input map[string]any) (*Link, error) {
	var out struct {
		Data []Link `json:"data"`
	}
	if err := c.do(ctx, http.MethodPost, "/links", input, &out); err != nil {
		return nil, err
	}
	if len(out.Data) == 0 {
		return nil, errors.New("RedirHub API created no link")
	}
	return &out.Data[0], nil
}

func (c *Client) GetLink(ctx context.Context, id string) (*Link, error) {
	var out Link
	if err := c.do(ctx, http.MethodGet, "/links/"+url.PathEscape(id), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) UpdateLink(ctx context.Context, id string, input map[string]any) (*Link, error) {
	var out Link
	if err := c.do(ctx, http.MethodPut, "/links/"+url.PathEscape(id), input, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) DeleteLink(ctx context.Context, id string) error {
	err := c.do(ctx, http.MethodDelete, "/links/"+url.PathEscape(id), nil, nil)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	return err
}

// ListLinks pages through `GET /links` with the given `filter[...]` values.
func (c *Client) ListLinks(ctx context.Context, filter map[string]string) ([]Link, error) {
	query := url.Values{"limit": {"100"}}
	for k, v := range filter {
		query.Set("filter["+k+"]", v)
	}

	var links []Link
	for {
		var page struct {
			Data []Link `json:"data"`
			Meta struct {
				Cursor struct {
					Next *string `json:"next"`
				} `json:"cursor"`
			} `json:"meta"`
		}
		if err := c.do(ctx, http.MethodGet, "/links?"+query.Encode(), nil, &page); err != nil {
			return nil, err
		}
		links = append(links, page.Data...)
		if page.Meta.Cursor.Next == nil || *page.Meta.Cursor.Next == "" {
			return links, nil
		}
		query.Set("cursor", *page.Meta.Cursor.Next)
	}
}

// DNSRecord is a record a domain needs to point to RedirHub.
type DNSRecord struct {
	Scope string `json:"scope"`
	Name  string `json:"name"`
	Type  string `json:"type"`
	Value string `json:"value"`
}

// Domain is a hostname connected to the workspace.
type Domain struct {
	Host              string      `json:"host"`
	Correct           bool        `json:"correct"`
	DefaultRequire    string      `json:"default_require"`
	Require           []DNSRecord `json:"require"`
	HTTPSRequested    bool        `json:"https_requested"`
	ForceHTTPS        bool        `json:"force_https"`
	ShortLinksEnabled bool        `json:"short_links_enabled"`
}

func (c *Client) CreateDomain(ctx context.Context, input map[string]any) (*Domain, error) {
	var out Domain
	if err := c.do(ctx, http.MethodPost, "/host", input, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) GetDomain(ctx context.Context, host string) (*Domain, error) {
	var out Domain
	if err := c.do(ctx, http.MethodGet, "/host/"+url.PathEscape(host), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// RefreshDomain checks the domain's DNS again and returns the result.
func (c *Client) RefreshDomain(ctx context.Context, host string) (*Domain, error) {
	var out Domain
	if err := c.do(ctx, http.MethodPost, "/host/"+url.PathEscape(host)+"/refresh", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) UpdateDomain(ctx context.Context, host string, input map[string]any) (*Domain, error) {
	var out Domain
	if err := c.do(ctx, http.MethodPut, "/host/"+url.PathEscape(host), input, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) DeleteDomain(ctx context.Context, host string) error {
	err := c.do(ctx, http.MethodDelete, "/host/"+url.PathEscape(host), nil, nil)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	return err
}
