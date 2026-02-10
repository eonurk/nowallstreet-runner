package indexer

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type Client struct {
	BaseURL  string
	HTTP     *http.Client
	OwnerUID string
}

type Agent struct {
	AgentID         string `json:"agent_id"`
	AgentAddr       string `json:"agent_addr"`
	UserAddr        string `json:"user_addr"`
	Status          string `json:"status"`
	StrategyHash    string `json:"strategy_hash"`
	StrategyURI     string `json:"strategy_uri"`
	StrategyVersion string `json:"strategy_version"`
	StrategyPrompt  string `json:"strategy_prompt"`
	Policy          struct {
		AllowedTokens []string `json:"allowed_tokens"`
	} `json:"policy"`
}

type Token struct {
	Symbol      string  `json:"symbol"`
	Name        string  `json:"name"`
	PriceAGC    float64 `json:"price_agc"`
	Change24H   float64 `json:"change_24h"`
	Volume24H   float64 `json:"volume_24h"`
	Supply      uint64  `json:"supply"`
	Holders     int     `json:"holders"`
	LastTradeAt string  `json:"last_trade_at"`
}

type Offer struct {
	OfferID   string  `json:"offer_id"`
	AgentID   string  `json:"agent_id"`
	Category  string  `json:"category"`
	PriceAGC  float64 `json:"price_agc"`
	Qty       float64 `json:"qty"`
	Status    string  `json:"status"`
	Asset     string  `json:"asset_symbol"`
	CreatedAt string  `json:"created_at"`
}

type RFQ struct {
	RFQID       string  `json:"rfq_id"`
	AgentID     string  `json:"agent_id"`
	Category    string  `json:"category"`
	MaxPriceAGC float64 `json:"max_price_agc"`
	Qty         float64 `json:"qty"`
	Status      string  `json:"status"`
	Asset       string  `json:"asset_symbol"`
	CreatedAt   string  `json:"created_at"`
}

type BalanceItem struct {
	Addr   string `json:"addr"`
	Denom  string `json:"denom"`
	Amount uint64 `json:"amount"`
}

type DevActionRequest struct {
	Action      string  `json:"action"`
	AgentID     string  `json:"agent_id"`
	AssetSymbol string  `json:"asset_symbol"`
	Category    string  `json:"category"`
	PriceAGC    float64 `json:"price_agc"`
	Qty         float64 `json:"qty"`
	Side        string  `json:"side"`
	Reason      string  `json:"reason"`
}

type DevDecisionRequest struct {
	AgentID     string  `json:"agent_id"`
	Action      string  `json:"action"`
	AssetSymbol string  `json:"asset_symbol"`
	PriceAGC    float64 `json:"price_agc"`
	Qty         float64 `json:"qty"`
	Side        string  `json:"side"`
	Reason      string  `json:"reason"`
	Raw         string  `json:"raw"`
	Status      string  `json:"status"`
	Error       string  `json:"error"`
}

type DevHeartbeatRequest struct {
	AgentID  string `json:"agent_id"`
	Profile  string `json:"profile"`
	UserAddr string `json:"user_addr"`
}

type Decision struct {
	DecisionID  string  `json:"decision_id"`
	AgentID     string  `json:"agent_id"`
	Action      string  `json:"action"`
	AssetSymbol string  `json:"asset_symbol"`
	PriceAGC    float64 `json:"price_agc"`
	Qty         float64 `json:"qty"`
	Side        string  `json:"side"`
	Reason      string  `json:"reason"`
	Status      string  `json:"status"`
	Error       string  `json:"error"`
	CreatedAt   string  `json:"created_at"`
}

type AgentHistory struct {
	Decisions []Decision `json:"decisions"`
}

func New(baseURL string, ownerUID ...string) *Client {
	uid := ""
	if len(ownerUID) > 0 {
		uid = strings.TrimSpace(ownerUID[0])
	}
	return &Client{
		BaseURL: strings.TrimRight(baseURL, "/"),
		HTTP: &http.Client{
			Timeout: 10 * time.Second,
		},
		OwnerUID: uid,
	}
}

func (c *Client) attachOwnerHeader(req *http.Request) {
	if req == nil {
		return
	}
	if strings.TrimSpace(c.OwnerUID) != "" {
		req.Header.Set("X-Auth-UID", strings.TrimSpace(c.OwnerUID))
	}
}

func (c *Client) GetAgent(ctx context.Context, agentID string) (Agent, error) {
	var agent Agent
	if err := c.fetchJSON(ctx, "/v1/agents/"+agentID, &agent); err != nil {
		return Agent{}, err
	}
	return agent, nil
}

func (c *Client) GetTokens(ctx context.Context) ([]Token, error) {
	var tokens []Token
	if err := c.fetchJSON(ctx, "/v1/tokens", &tokens); err != nil {
		return nil, err
	}
	return tokens, nil
}

func (c *Client) GetOffers(ctx context.Context) ([]Offer, error) {
	var offers []Offer
	if err := c.fetchJSON(ctx, "/v1/offers", &offers); err != nil {
		return nil, err
	}
	return offers, nil
}

func (c *Client) GetRFQs(ctx context.Context) ([]RFQ, error) {
	var rfqs []RFQ
	if err := c.fetchJSON(ctx, "/v1/rfqs", &rfqs); err != nil {
		return nil, err
	}
	return rfqs, nil
}

func (c *Client) GetBalances(ctx context.Context, addr string) (map[string]uint64, error) {
	var items []BalanceItem
	if err := c.fetchJSON(ctx, "/v1/balances/"+addr, &items); err != nil {
		return nil, err
	}
	out := map[string]uint64{}
	for _, item := range items {
		if item.Denom == "" {
			continue
		}
		out[item.Denom] = item.Amount
	}
	return out, nil
}

func (c *Client) GetAgentHistory(ctx context.Context, agentID string) (AgentHistory, error) {
	var history AgentHistory
	if err := c.fetchJSON(ctx, "/v1/agents/"+agentID+"/history", &history); err != nil {
		return AgentHistory{}, err
	}
	return history, nil
}

func (c *Client) PostDevAction(ctx context.Context, req DevActionRequest) error {
	body, err := json.Marshal(req)
	if err != nil {
		return err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/v1/dev/actions", strings.NewReader(string(body)))
	if err != nil {
		return err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	c.attachOwnerHeader(httpReq)
	resp, err := c.HTTP.Do(httpReq)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		msg := "indexer request failed"
		if body, err := io.ReadAll(io.LimitReader(resp.Body, 4096)); err == nil {
			trimmed := strings.TrimSpace(string(body))
			if trimmed != "" {
				msg = fmt.Sprintf("%s: %s", msg, trimmed)
			}
		}
		return fmt.Errorf("%s (status %d)", msg, resp.StatusCode)
	}
	return nil
}

func (c *Client) PostDevDecision(ctx context.Context, req DevDecisionRequest) error {
	body, err := json.Marshal(req)
	if err != nil {
		return err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/v1/dev/decisions", strings.NewReader(string(body)))
	if err != nil {
		return err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	c.attachOwnerHeader(httpReq)
	resp, err := c.HTTP.Do(httpReq)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		msg := "indexer request failed"
		if body, err := io.ReadAll(io.LimitReader(resp.Body, 4096)); err == nil {
			trimmed := strings.TrimSpace(string(body))
			if trimmed != "" {
				msg = fmt.Sprintf("%s: %s", msg, trimmed)
			}
		}
		return fmt.Errorf("%s (status %d)", msg, resp.StatusCode)
	}
	return nil
}

func (c *Client) PostDevHeartbeat(ctx context.Context, req DevHeartbeatRequest) error {
	body, err := json.Marshal(req)
	if err != nil {
		return err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/v1/dev/heartbeat", strings.NewReader(string(body)))
	if err != nil {
		return err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	c.attachOwnerHeader(httpReq)
	resp, err := c.HTTP.Do(httpReq)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		msg := "indexer request failed"
		if body, err := io.ReadAll(io.LimitReader(resp.Body, 4096)); err == nil {
			trimmed := strings.TrimSpace(string(body))
			if trimmed != "" {
				msg = fmt.Sprintf("%s: %s", msg, trimmed)
			}
		}
		return fmt.Errorf("%s (status %d)", msg, resp.StatusCode)
	}
	return nil
}

func (c *Client) fetchJSON(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+path, nil)
	if err != nil {
		return err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		msg := "indexer request failed"
		if body, err := io.ReadAll(io.LimitReader(resp.Body, 4096)); err == nil {
			trimmed := strings.TrimSpace(string(body))
			if trimmed != "" {
				msg = fmt.Sprintf("%s: %s", msg, trimmed)
			}
		}
		return fmt.Errorf("%s (status %d)", msg, resp.StatusCode)
	}

	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return err
	}
	return nil
}
