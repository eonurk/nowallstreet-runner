package registrar

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type Client struct {
	BaseURL string
	HTTP    *http.Client
}

type Invoice struct {
	InvoiceID    string `json:"invoice_id"`
	Bolt11       string `json:"bolt11"`
	AmountSats   uint64 `json:"amount_sats"`
	ExpiresAt    string `json:"expires_at"`
	Status       string `json:"status"`
	PaidAt       string `json:"paid_at,omitempty"`
	ChainTxHash  string `json:"chain_tx_hash,omitempty"`
	RegisteredAt string `json:"registered_at,omitempty"`
}

type CreateInvoiceRequest struct {
	UserAddr  string `json:"user_addr"`
	AgentAddr string `json:"agent_addr"`
}

func New(baseURL string) *Client {
	return &Client{
		BaseURL: strings.TrimRight(baseURL, "/"),
		HTTP: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

func (c *Client) CreateInvoice(ctx context.Context, userAddr, agentAddr string) (Invoice, error) {
	payload := CreateInvoiceRequest{UserAddr: userAddr, AgentAddr: agentAddr}
	body, err := json.Marshal(payload)
	if err != nil {
		return Invoice{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/v1/invoices", bytes.NewReader(body))
	if err != nil {
		return Invoice{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	return c.do(req)
}

func (c *Client) GetInvoice(ctx context.Context, invoiceID string) (Invoice, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/v1/invoices/"+invoiceID, nil)
	if err != nil {
		return Invoice{}, err
	}
	return c.do(req)
}

func (c *Client) do(req *http.Request) (Invoice, error) {
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return Invoice{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		msg := "registrar request failed"
		if body, err := io.ReadAll(io.LimitReader(resp.Body, 4096)); err == nil {
			trimmed := strings.TrimSpace(string(body))
			if trimmed != "" {
				msg = fmt.Sprintf("%s: %s", msg, trimmed)
			}
		}
		return Invoice{}, fmt.Errorf("%s (status %d)", msg, resp.StatusCode)
	}

	var invoice Invoice
	if err := json.NewDecoder(resp.Body).Decode(&invoice); err != nil {
		return Invoice{}, err
	}
	return invoice, nil
}
