package httpx

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

var ErrBodyTooLarge = errors.New("response body exceeded maximum size")

type Response struct {
	URL        string
	StatusCode int
	Header     http.Header
	Body       []byte
}

type Client struct {
	httpClient *http.Client
	userAgent  string
}

func New(timeout time.Duration, userAgent string) *Client {
	return &Client{
		httpClient: &http.Client{
			Timeout: timeout,
		},
		userAgent: userAgent,
	}
}

func (c *Client) FetchTail(ctx context.Context, targetURL string, tailBytes int64, maxBytes int64) (*Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, targetURL, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Range", fmt.Sprintf("bytes=-%d", tailBytes))
	req.Header.Set("User-Agent", c.userAgent)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := readLastN(resp.Body, tailBytes, maxBytes)
	if err != nil {
		return nil, err
	}

	return &Response{
		URL:        resp.Request.URL.String(),
		StatusCode: resp.StatusCode,
		Header:     resp.Header.Clone(),
		Body:       body,
	}, nil
}

func (c *Client) FetchAll(ctx context.Context, targetURL string, maxBytes int64) (*Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, targetURL, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("User-Agent", c.userAgent)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := readAllLimited(resp.Body, maxBytes)
	if err != nil {
		return nil, err
	}

	return &Response{
		URL:        resp.Request.URL.String(),
		StatusCode: resp.StatusCode,
		Header:     resp.Header.Clone(),
		Body:       body,
	}, nil
}

func readAllLimited(r io.Reader, maxBytes int64) ([]byte, error) {
	limited := io.LimitReader(r, maxBytes+1)
	body, err := io.ReadAll(limited)
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > maxBytes {
		return nil, ErrBodyTooLarge
	}
	return body, nil
}

func readLastN(r io.Reader, tailBytes int64, maxBytes int64) ([]byte, error) {
	if tailBytes <= 0 {
		return nil, nil
	}

	buf := make([]byte, 0, tailBytes)
	tmp := make([]byte, 32*1024)
	var total int64

	for {
		n, err := r.Read(tmp)
		if n > 0 {
			total += int64(n)
			if total > maxBytes {
				return nil, ErrBodyTooLarge
			}

			buf = append(buf, tmp[:n]...)
			if int64(len(buf)) > tailBytes {
				buf = append([]byte(nil), buf[int64(len(buf))-tailBytes:]...)
			}
		}

		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
	}

	return buf, nil
}
