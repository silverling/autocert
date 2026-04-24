package cloudflare

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const defaultBaseURL = "https://api.cloudflare.com/client/v4"

type Client struct {
	authToken  string
	baseURL    *url.URL
	httpClient *http.Client
}

type ClientConfig struct {
	AuthToken  string
	BaseURL    string
	HTTPClient *http.Client
}

func NewClient(cfg ClientConfig) (*Client, error) {
	if strings.TrimSpace(cfg.AuthToken) == "" {
		return nil, fmt.Errorf("cloudflare auth token is required")
	}

	baseURL := strings.TrimSpace(cfg.BaseURL)
	if baseURL == "" {
		baseURL = defaultBaseURL
	}

	parsedBaseURL, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("parse Cloudflare base URL: %w", err)
	}

	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}

	return &Client{
		authToken:  cfg.AuthToken,
		baseURL:    parsedBaseURL,
		httpClient: httpClient,
	}, nil
}

func (c *Client) ZoneIDByName(ctx context.Context, name string) (string, error) {
	endpoint := c.baseURL.JoinPath("zones")

	query := endpoint.Query()
	query.Set("name", strings.TrimSuffix(strings.TrimSpace(name), "."))
	query.Set("per_page", "50")
	endpoint.RawQuery = query.Encode()

	req, err := newJSONRequest(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", err
	}

	var result APIResponse[[]Zone]
	if err := c.do(req, &result); err != nil {
		return "", err
	}

	switch len(result.Result) {
	case 0:
		return "", fmt.Errorf("zone could not be found")
	case 1:
		return result.Result[0].ID, nil
	default:
		return "", fmt.Errorf("ambiguous zone name")
	}
}

func (c *Client) CreateDNSRecord(ctx context.Context, zoneID string, record Record) (*Record, error) {
	endpoint := c.baseURL.JoinPath("zones", zoneID, "dns_records")

	req, err := newJSONRequest(ctx, http.MethodPost, endpoint, record)
	if err != nil {
		return nil, err
	}

	var result APIResponse[Record]
	if err := c.do(req, &result); err != nil {
		return nil, err
	}

	return &result.Result, nil
}

func (c *Client) DeleteDNSRecord(ctx context.Context, zoneID, recordID string) error {
	endpoint := c.baseURL.JoinPath("zones", zoneID, "dns_records", recordID)

	req, err := newJSONRequest(ctx, http.MethodDelete, endpoint, nil)
	if err != nil {
		return err
	}

	return c.do(req, nil)
}

func (c *Client) do(req *http.Request, result any) error {
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.authToken)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode/100 != 2 {
		raw, _ := io.ReadAll(resp.Body)

		var apiErr APIResponse[any]
		if err := json.Unmarshal(raw, &apiErr); err == nil && len(apiErr.Errors) > 0 {
			return fmt.Errorf("[status code %d] %w", resp.StatusCode, apiErr.Errors)
		}

		return fmt.Errorf("[status code %d] %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}

	if result == nil {
		return nil
	}

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	if err := json.Unmarshal(raw, result); err != nil {
		return err
	}

	return nil
}

func newJSONRequest(ctx context.Context, method string, endpoint *url.URL, payload any) (*http.Request, error) {
	body := bytes.NewBuffer(nil)
	if payload != nil {
		if err := json.NewEncoder(body).Encode(payload); err != nil {
			return nil, fmt.Errorf("encode JSON body: %w", err)
		}
	}

	req, err := http.NewRequestWithContext(ctx, method, endpoint.String(), body)
	if err != nil {
		return nil, fmt.Errorf("create HTTP request: %w", err)
	}

	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	return req, nil
}
