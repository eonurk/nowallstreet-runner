package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"math"
	"sort"
	"strings"
	"time"

	"agentmarket/agent/internal/indexer"
	"agentmarket/agent/internal/llm"
)

type Action struct {
	Action       string  `json:"action"`
	AssetSymbol  string  `json:"asset_symbol"`
	Category     string  `json:"category"`
	PriceAGC     float64 `json:"price_agc"`
	Qty          float64 `json:"qty"`
	Side         string  `json:"side"`
	Reason       string  `json:"reason"`
	NextCheckSec int     `json:"next_check_sec"`
}

const (
	maxOpenOffersPerAgent = 5
	maxOpenOffersPerAsset = 3
	maxOpenRFQsPerAgent   = 3
	decisionMaxAttempts   = 3
	decisionMemoryLimit   = 12
	decisionSeedLimit     = 8
	defaultWaitSec        = 6
	minWaitSec            = 1
	maxWaitSec            = 60
)

var (
	offerFeeAGC                uint64 = 0
	rfqFeeAGC                  uint64 = 0
	tradeFeeBps                uint64 = 10
	syntheticMintFeePerUnitAGC uint64 = 0
)

type Runner struct {
	Tick           time.Duration
	AgentID        string
	UserAddr       string
	LLM            llm.Client
	Indexer        *indexer.Client
	Profile        string
	StrategyPrompt string
	lastBalances   map[string]uint64
	lastTokenPrice map[string]float64
	lastOffers     []indexer.Offer
	lastRFQs       []indexer.RFQ
	lastOpenOffers int
	lastOpenRFQs   int
	lastOffersByAS map[string]int
	allowedTokens  []string
	lastAgentSync  time.Time
	cycle          uint64
	decisionMemory []memoryDecision
	memorySeeded   bool
}

type memoryDecision struct {
	Action      string
	AssetSymbol string
	Side        string
	PriceAGC    float64
	Qty         float64
	Status      string
	Error       string
	Reason      string
	CreatedAt   string
	Reward      float64
}

func NewRunner(agentID string, client llm.Client, idx *indexer.Client) *Runner {
	return &Runner{
		Tick:           2 * time.Second,
		AgentID:        agentID,
		LLM:            client,
		Indexer:        idx,
		Profile:        resolveProfile(agentID, ""),
		lastTokenPrice: map[string]float64{},
		lastOffersByAS: map[string]int{},
	}
}

func NewRunnerWithProfile(agentID, userAddr string, client llm.Client, idx *indexer.Client, profile string) *Runner {
	return &Runner{
		Tick:           2 * time.Second,
		AgentID:        agentID,
		UserAddr:       strings.TrimSpace(userAddr),
		LLM:            client,
		Indexer:        idx,
		Profile:        resolveProfile(agentID, profile),
		lastTokenPrice: map[string]float64{},
		lastOffersByAS: map[string]int{},
	}
}

func (r *Runner) Run(ctx context.Context) error {
	ticker := time.NewTicker(r.Tick)
	defer ticker.Stop()
	r.postHeartbeat(ctx)
	nextDecisionAt := time.Now()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			r.cycle++
			r.postHeartbeat(ctx)
			if time.Now().Before(nextDecisionAt) {
				continue
			}
			if r.LLM == nil {
				r.postDecision(ctx, Action{Action: "invalid", Reason: "no_llm"}, "rejected", "no llm configured", "")
				nextDecisionAt = time.Now().Add(5 * time.Second)
				continue
			}
			r.refreshBalances(ctx)
			r.seedDecisionMemory(ctx)
			prompt := r.buildPrompt(ctx)
			action, raw, err := r.decideStrict(ctx, prompt)
			if err != nil {
				fmt.Printf("strict decision error (%s/%s): %v\n", r.LLM.Provider(), r.LLM.Model(), err)
				r.postDecision(ctx, Action{Action: "invalid", Reason: "decision_error"}, "rejected", err.Error(), raw)
				nextDecisionAt = time.Now().Add(3 * time.Second)
				continue
			}
			if strings.EqualFold(action.Action, "wait") {
				if strings.TrimSpace(action.Reason) == "" {
					action.Reason = "model_wait"
				}
				waitFor := normalizeWaitDuration(action.NextCheckSec)
				r.postDecision(ctx, action, "wait", "", raw)
				nextDecisionAt = time.Now().Add(waitFor)
				continue
			}
			r.executeAction(ctx, action, raw)
			nextDecisionAt = time.Now().Add(r.Tick)
		}
	}
}

func (r *Runner) decideStrict(ctx context.Context, basePrompt llm.Prompt) (Action, string, error) {
	prompt := basePrompt
	lastRaw := ""
	lastErr := "no decision produced"

	for attempt := 1; attempt <= decisionMaxAttempts; attempt++ {
		response, err := r.LLM.Generate(ctx, prompt)
		if err != nil {
			lastErr = fmt.Sprintf("llm error: %v", err)
		} else {
			raw := strings.TrimSpace(response)
			lastRaw = raw
			fmt.Printf("llm decision attempt %d (%s/%s): %s\n", attempt, r.LLM.Provider(), r.LLM.Model(), raw)
			action, parseErr := parseAction(raw)
			if parseErr != nil {
				lastErr = fmt.Sprintf("parse error: %v", parseErr)
			} else {
				normalizeAction(&action)
				r.repairAction(&action)
				if validationErr := validateStrictAction(action); validationErr == "" {
					return action, raw, nil
				} else {
					lastErr = validationErr
				}
			}
		}

		if attempt < decisionMaxAttempts {
			prompt = strictRetryPrompt(basePrompt, lastErr, attempt)
		}
	}

	return Action{}, lastRaw, fmt.Errorf("failed to produce strict action after %d attempts: %s", decisionMaxAttempts, lastErr)
}

func validateStrictAction(action Action) string {
	act := strings.ToLower(strings.TrimSpace(action.Action))
	switch act {
	case "post_offer", "create_rfq", "trade", "wait":
	default:
		if act == "" {
			return "missing action"
		}
		if act == "noop" {
			return "noop is not allowed"
		}
		return fmt.Sprintf("invalid action: %s", action.Action)
	}

	if act == "wait" {
		if action.NextCheckSec < 0 {
			return "next_check_sec must be >= 0"
		}
		return ""
	}

	asset := strings.ToUpper(strings.TrimSpace(action.AssetSymbol))
	if asset == "" {
		return "asset_symbol is required"
	}
	if asset == "AGC" {
		return "asset_symbol must not be AGC"
	}
	if action.Qty <= 0 {
		return "qty must be > 0"
	}

	if act == "trade" {
		side := strings.ToLower(strings.TrimSpace(action.Side))
		if side != "buy" && side != "sell" {
			return "trade side must be buy or sell"
		}
	}
	if (act == "post_offer" || act == "create_rfq") && action.PriceAGC <= 0 {
		return "price_agc must be > 0"
	}
	return ""
}

func strictRetryPrompt(base llm.Prompt, reason string, attempt int) llm.Prompt {
	addendum := fmt.Sprintf(
		"\nPrevious output was rejected (%s). Attempt %d/%d. "+
			"Return exactly one JSON object with action in ['post_offer','create_rfq','trade','wait']. "+
			"For wait, provide next_check_sec (1-60). For trade, include side. No noop, no markdown.",
		strings.TrimSpace(reason),
		attempt+1,
		decisionMaxAttempts,
	)
	return llm.Prompt{
		System: base.System,
		User:   base.User + addendum,
	}
}

func normalizeWaitDuration(sec int) time.Duration {
	if sec <= 0 {
		sec = defaultWaitSec
	}
	if sec < minWaitSec {
		sec = minWaitSec
	}
	if sec > maxWaitSec {
		sec = maxWaitSec
	}
	return time.Duration(sec) * time.Second
}

func (r *Runner) repairAction(action *Action) {
	if action == nil {
		return
	}
	act := strings.ToLower(strings.TrimSpace(action.Action))
	if act == "" || act == "wait" || act == "noop" {
		return
	}

	if strings.TrimSpace(action.AssetSymbol) == "" {
		action.AssetSymbol = r.pickActionAsset(act)
	}
	if strings.TrimSpace(action.AssetSymbol) == "" {
		return
	}

	if action.Qty <= 0 {
		assetBal := uint64(0)
		if r.lastBalances != nil {
			assetBal = r.lastBalances[strings.ToUpper(strings.TrimSpace(action.AssetSymbol))]
		}
		if assetBal > 0 {
			action.Qty = math.Max(1, math.Min(5, float64(assetBal)))
		} else {
			action.Qty = 1
		}
	}

	if (act == "post_offer" || act == "create_rfq" || act == "trade") && action.PriceAGC <= 0 {
		price := r.lastTokenPrice[strings.ToUpper(strings.TrimSpace(action.AssetSymbol))]
		if price > 0 {
			action.PriceAGC = price
		} else {
			action.PriceAGC = 1
		}
	}

	if act == "trade" {
		side := strings.ToLower(strings.TrimSpace(action.Side))
		if side != "buy" && side != "sell" {
			assetBal := uint64(0)
			if r.lastBalances != nil {
				assetBal = r.lastBalances[strings.ToUpper(strings.TrimSpace(action.AssetSymbol))]
			}
			if assetBal > 0 {
				action.Side = "sell"
			} else {
				action.Side = "buy"
			}
		}
	}
}

func (r *Runner) pickActionAsset(action string) string {
	allowed := map[string]struct{}{}
	for _, symbol := range r.allowedTokens {
		clean := strings.ToUpper(strings.TrimSpace(symbol))
		if clean == "" || clean == "AGC" {
			continue
		}
		allowed[clean] = struct{}{}
	}
	accept := func(symbol string) bool {
		clean := strings.ToUpper(strings.TrimSpace(symbol))
		if clean == "" || clean == "AGC" {
			return false
		}
		if len(allowed) == 0 {
			return true
		}
		_, ok := allowed[clean]
		return ok
	}

	if action == "post_offer" || action == "trade" {
		best := ""
		bestQty := uint64(0)
		for symbol, amount := range r.lastBalances {
			clean := strings.ToUpper(strings.TrimSpace(symbol))
			if !accept(clean) || amount == 0 {
				continue
			}
			if amount > bestQty {
				best = clean
				bestQty = amount
			}
		}
		if best != "" {
			return best
		}
	}

	for symbol := range r.lastTokenPrice {
		clean := strings.ToUpper(strings.TrimSpace(symbol))
		if accept(clean) {
			return clean
		}
	}
	for symbol := range allowed {
		if symbol != "AGC" {
			return symbol
		}
	}
	return ""
}

func (r *Runner) executeAction(ctx context.Context, action Action, raw string) {
	if status, errMsg := r.preflight(action); status != "" {
		r.postDecision(ctx, action, status, errMsg, raw)
		return
	}
	if r.Indexer == nil {
		r.postDecision(ctx, action, "rejected", "no indexer configured", raw)
		fmt.Println("no indexer configured for action execution")
		return
	}

	req := indexer.DevActionRequest{
		Action:      strings.ToLower(strings.TrimSpace(action.Action)),
		AgentID:     r.AgentID,
		AssetSymbol: strings.ToUpper(strings.TrimSpace(action.AssetSymbol)),
		Category:    strings.TrimSpace(action.Category),
		PriceAGC:    action.PriceAGC,
		Qty:         action.Qty,
		Side:        strings.ToLower(strings.TrimSpace(action.Side)),
		Reason:      strings.TrimSpace(action.Reason),
	}

	execCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	err := r.Indexer.PostDevAction(execCtx, req)
	cancel()
	if err != nil {
		r.postDecision(ctx, action, "rejected", err.Error(), raw)
		fmt.Printf("action failed: %v\n", err)
		return
	}
	r.postDecision(ctx, action, "executed", "", raw)
	fmt.Printf("action executed: %s %s\n", req.Action, req.AssetSymbol)
}

func (r *Runner) buildPrompt(ctx context.Context) llm.Prompt {
	system := "You are an autonomous market agent. Reply with a single JSON object only. " +
		"Schema: {action: 'post_offer' | 'create_rfq' | 'trade' | 'wait', asset_symbol?: string, price_agc?: number, qty?: number, side?: 'buy' | 'sell', next_check_sec?: number, reason?: string}. " +
		"Never return noop. If waiting, set action='wait' with next_check_sec (1-60)."
	r.refreshAgentConfig(ctx)
	if strings.TrimSpace(r.StrategyPrompt) != "" {
		system += " Custom strategy instructions from user: " + strings.TrimSpace(r.StrategyPrompt)
	}

	user := "No market snapshot available. Return {\"action\":\"wait\",\"next_check_sec\":5,\"reason\":\"market_unavailable\"}."
	if r.Indexer == nil {
		return llm.Prompt{System: system, User: user}
	}

	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	tokens, err := r.Indexer.GetTokens(ctx)
	if err != nil {
		return llm.Prompt{System: system, User: user}
	}
	offers, _ := r.Indexer.GetOffers(ctx)
	rfqs, _ := r.Indexer.GetRFQs(ctx)
	r.updateTokenPrices(tokens)
	r.lastOffers = offers
	r.lastRFQs = rfqs

	entries := make([]string, 0, 6)
	for i, token := range tokens {
		if i >= 6 {
			break
		}
		entries = append(entries, fmt.Sprintf("%s %.2f (%+.2f%%)", token.Symbol, token.PriceAGC, token.Change24H))
	}

	openOffers := 0
	openRFQs := 0
	openByAsset := map[string]int{}
	for _, offer := range offers {
		if offer.AgentID == r.AgentID && (offer.Status == "" || offer.Status == "open") {
			openOffers++
			symbol := strings.ToUpper(strings.TrimSpace(offer.Asset))
			if symbol != "" {
				openByAsset[symbol]++
			}
		}
	}
	for _, rfq := range rfqs {
		if rfq.AgentID == r.AgentID && (rfq.Status == "" || rfq.Status == "open") {
			openRFQs++
		}
	}
	r.lastOpenOffers = openOffers
	r.lastOpenRFQs = openRFQs
	r.lastOffersByAS = openByAsset

	holdings := r.formatHoldings()
	profileGuide := profilePrompt(r.Profile)
	allowedSummary := "any listed token except AGC"
	if len(r.allowedTokens) > 0 {
		allowedSummary = strings.Join(r.allowedTokens, ", ")
	}
	memorySummary := r.memorySummary()
	learningSummary := r.memoryLessons()
	opportunitySummary := summarizeOrderbook(tokens, offers, rfqs, r.AgentID, r.allowedTokens)
	user = fmt.Sprintf(
		"Agent %s (%s). Market snapshot: tokens [%s]. Offers: %d. RFQs: %d. Holdings: %s. "+
			"You currently have %d open offers and %d open RFQs. Do not exceed 5 offers or 3 RFQs. "+
			"Allowed asset symbols: [%s]. "+
			"Never use AGC as asset_symbol; AGC is settlement only. "+
			"Do not post offers for assets you don't own. If you only hold AGC, start with trade buy or RFQ. "+
			"Orderbook lens: %s. "+
			"Recent decision memory: %s. "+
			"Learning hints: %s. "+
			"You must decide one JSON action now: either execute (post_offer/create_rfq/trade) or wait with next_check_sec. %s Choose one action.",
		r.AgentID, r.Profile, strings.Join(entries, ", "), len(offers), len(rfqs), holdings, openOffers, openRFQs, allowedSummary, opportunitySummary, memorySummary, learningSummary, profileGuide,
	)

	return llm.Prompt{System: system, User: user}
}

func parseAction(raw string) (Action, error) {
	clean := strings.TrimSpace(raw)
	if strings.HasPrefix(clean, "```") {
		clean = strings.TrimPrefix(clean, "```")
		clean = strings.TrimSuffix(clean, "```")
		clean = strings.TrimSpace(clean)
	}
	if !strings.HasPrefix(clean, "{") {
		start := strings.Index(clean, "{")
		end := strings.LastIndex(clean, "}")
		if start >= 0 && end > start {
			clean = clean[start : end+1]
		}
	}
	var action Action
	if err := json.Unmarshal([]byte(clean), &action); err != nil {
		return Action{}, err
	}
	return action, nil
}

func resolveProfile(agentID, requested string) string {
	requested = strings.ToLower(strings.TrimSpace(requested))
	if requested != "" {
		return requested
	}
	if agentID == "" {
		return "market_maker"
	}
	hash := fnv.New32a()
	_, _ = hash.Write([]byte(agentID))
	switch hash.Sum32() % 3 {
	case 0:
		return "market_maker"
	case 1:
		return "taker"
	default:
		return "momentum"
	}
}

func profilePrompt(profile string) string {
	switch profile {
	case "market_maker":
		return "You are a market maker. Post tight offers near current price with small qty to earn spread."
	case "taker":
		return "You are a taker. Prefer trades or RFQs over posting many offers."
	case "momentum":
		return "You are momentum-biased. If change_24h is positive, prefer buy; if negative, prefer sell."
	default:
		return "Be cautious and prefer small actions."
	}
}

func (r *Runner) postDecision(ctx context.Context, action Action, status, errMsg, raw string) {
	r.appendDecisionMemory(action, status, errMsg)
	if r.Indexer == nil {
		return
	}
	req := indexer.DevDecisionRequest{
		AgentID:     r.AgentID,
		Action:      strings.ToLower(strings.TrimSpace(action.Action)),
		AssetSymbol: strings.ToUpper(strings.TrimSpace(action.AssetSymbol)),
		PriceAGC:    action.PriceAGC,
		Qty:         action.Qty,
		Side:        strings.ToLower(strings.TrimSpace(action.Side)),
		Reason:      strings.TrimSpace(action.Reason),
		Raw:         strings.TrimSpace(raw),
		Status:      status,
		Error:       strings.TrimSpace(errMsg),
	}
	execCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	_ = r.Indexer.PostDevDecision(execCtx, req)
	cancel()
}

func (r *Runner) postHeartbeat(ctx context.Context) {
	if r.Indexer == nil || strings.TrimSpace(r.AgentID) == "" {
		return
	}
	req := indexer.DevHeartbeatRequest{
		AgentID:  strings.TrimSpace(r.AgentID),
		Profile:  strings.TrimSpace(r.Profile),
		UserAddr: strings.TrimSpace(r.UserAddr),
	}
	execCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	_ = r.Indexer.PostDevHeartbeat(execCtx, req)
	cancel()
}

func (r *Runner) refreshAgentConfig(ctx context.Context) {
	if r.Indexer == nil || strings.TrimSpace(r.AgentID) == "" {
		return
	}
	if !r.lastAgentSync.IsZero() && time.Since(r.lastAgentSync) < 5*time.Second {
		return
	}
	cfgCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	agentCfg, err := r.Indexer.GetAgent(cfgCtx, r.AgentID)
	cancel()
	r.lastAgentSync = time.Now()
	if err != nil {
		return
	}
	r.StrategyPrompt = strings.TrimSpace(agentCfg.StrategyPrompt)
	nextAllowed := make([]string, 0, len(agentCfg.Policy.AllowedTokens))
	for _, token := range agentCfg.Policy.AllowedTokens {
		symbol := strings.ToUpper(strings.TrimSpace(token))
		if symbol == "" || symbol == "AGC" {
			continue
		}
		nextAllowed = append(nextAllowed, symbol)
	}
	r.allowedTokens = nextAllowed
}

func (r *Runner) seedDecisionMemory(ctx context.Context) {
	if r.memorySeeded || r.Indexer == nil || strings.TrimSpace(r.AgentID) == "" {
		return
	}
	r.memorySeeded = true
	historyCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	history, err := r.Indexer.GetAgentHistory(historyCtx, r.AgentID)
	cancel()
	if err != nil || len(history.Decisions) == 0 {
		return
	}
	decisions := make([]indexer.Decision, 0, len(history.Decisions))
	for _, item := range history.Decisions {
		action := strings.ToLower(strings.TrimSpace(item.Action))
		if action == "" || action == "noop" {
			continue
		}
		decisions = append(decisions, item)
	}
	sort.SliceStable(decisions, func(i, j int) bool {
		a := strings.TrimSpace(decisions[i].CreatedAt)
		b := strings.TrimSpace(decisions[j].CreatedAt)
		if a == b {
			return decisions[i].DecisionID < decisions[j].DecisionID
		}
		return a < b
	})
	if len(decisions) > decisionSeedLimit {
		decisions = decisions[len(decisions)-decisionSeedLimit:]
	}
	for _, item := range decisions {
		r.pushDecisionMemory(memoryDecision{
			Action:      strings.ToLower(strings.TrimSpace(item.Action)),
			AssetSymbol: strings.ToUpper(strings.TrimSpace(item.AssetSymbol)),
			Side:        strings.ToLower(strings.TrimSpace(item.Side)),
			PriceAGC:    item.PriceAGC,
			Qty:         item.Qty,
			Status:      strings.ToLower(strings.TrimSpace(item.Status)),
			Error:       strings.TrimSpace(item.Error),
			Reason:      strings.TrimSpace(item.Reason),
			CreatedAt:   strings.TrimSpace(item.CreatedAt),
			Reward:      scoreDecisionOutcome(strings.TrimSpace(item.Status), strings.TrimSpace(item.Error)),
		})
	}
}

func (r *Runner) appendDecisionMemory(action Action, status, errMsg string) {
	r.pushDecisionMemory(memoryDecision{
		Action:      strings.ToLower(strings.TrimSpace(action.Action)),
		AssetSymbol: strings.ToUpper(strings.TrimSpace(action.AssetSymbol)),
		Side:        strings.ToLower(strings.TrimSpace(action.Side)),
		PriceAGC:    action.PriceAGC,
		Qty:         action.Qty,
		Status:      strings.ToLower(strings.TrimSpace(status)),
		Error:       strings.TrimSpace(errMsg),
		Reason:      strings.TrimSpace(action.Reason),
		CreatedAt:   time.Now().UTC().Format(time.RFC3339),
		Reward:      scoreDecisionOutcome(status, errMsg),
	})
}

func (r *Runner) pushDecisionMemory(entry memoryDecision) {
	if strings.TrimSpace(entry.Action) == "" {
		return
	}
	if strings.TrimSpace(entry.Status) == "" {
		entry.Status = "logged"
	}
	if strings.TrimSpace(entry.CreatedAt) == "" {
		entry.CreatedAt = time.Now().UTC().Format(time.RFC3339)
	}
	r.decisionMemory = append(r.decisionMemory, entry)
	if len(r.decisionMemory) > decisionMemoryLimit {
		r.decisionMemory = r.decisionMemory[len(r.decisionMemory)-decisionMemoryLimit:]
	}
}

func (r *Runner) memorySummary() string {
	if len(r.decisionMemory) == 0 {
		return "none yet"
	}
	start := 0
	if len(r.decisionMemory) > 6 {
		start = len(r.decisionMemory) - 6
	}
	parts := make([]string, 0, len(r.decisionMemory)-start)
	for _, item := range r.decisionMemory[start:] {
		action := item.Action
		if action == "" {
			action = "unknown"
		}
		asset := item.AssetSymbol
		if asset == "" {
			asset = "-"
		}
		side := item.Side
		if side == "" {
			side = "-"
		}
		status := item.Status
		if status == "" {
			status = "logged"
		}
		msg := fmt.Sprintf("%s %s %s q=%.2f p=%.2f => %s (%.1f)", action, asset, side, item.Qty, item.PriceAGC, status, item.Reward)
		if item.Error != "" {
			msg += " err=" + trimForPrompt(item.Error, 52)
		}
		parts = append(parts, msg)
	}
	return strings.Join(parts, " | ")
}

func (r *Runner) memoryLessons() string {
	if len(r.decisionMemory) == 0 {
		return "keep sizes small, prefer liquid symbols, and avoid invalid schema"
	}
	executed := 0
	waiting := 0
	failures := 0
	insufficient := 0
	liquidity := 0
	schema := 0
	limits := 0
	for _, item := range r.decisionMemory {
		status := strings.ToLower(strings.TrimSpace(item.Status))
		switch status {
		case "executed":
			executed++
		case "wait":
			waiting++
		case "blocked", "rejected":
			failures++
		}
		errMsg := strings.ToLower(strings.TrimSpace(item.Error))
		if strings.Contains(errMsg, "insufficient") {
			insufficient++
		}
		if strings.Contains(errMsg, "no matching") || strings.Contains(errMsg, "liquidity") {
			liquidity++
		}
		if strings.Contains(errMsg, "asset_symbol is required") || strings.Contains(errMsg, "invalid action") || strings.Contains(errMsg, "parse error") {
			schema++
		}
		if strings.Contains(errMsg, "limit reached") {
			limits++
		}
	}
	notes := []string{}
	if schema > 0 {
		notes = append(notes, "always return strict schema with action+asset_symbol+qty(+side for trade)")
	}
	if insufficient > 0 {
		notes = append(notes, "reduce qty or price to stay inside balances")
	}
	if liquidity > 0 {
		notes = append(notes, "prefer trade sizes that fit visible opposite liquidity")
	}
	if limits > 0 {
		notes = append(notes, "if limits are hit, wait or trade instead of creating new offers/RFQs")
	}
	if failures > executed {
		notes = append(notes, "failure rate high: prefer one conservative action over aggressive retries")
	}
	if executed > 0 {
		notes = append(notes, fmt.Sprintf("recently executed %d actions; reuse similar valid sizing", executed))
	}
	if waiting > 0 && executed == 0 {
		notes = append(notes, "waiting is acceptable, but seek a small executable trade when liquidity appears")
	}
	if len(notes) == 0 {
		return "execution quality stable; continue with small, policy-safe actions"
	}
	return strings.Join(notes, "; ")
}

func scoreDecisionOutcome(status, errMsg string) float64 {
	score := -0.1
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "executed":
		score = 0.8
	case "wait":
		score = 0.2
	case "blocked":
		score = -0.3
	case "rejected":
		score = -0.7
	}
	errLower := strings.ToLower(strings.TrimSpace(errMsg))
	if errLower == "" {
		return score
	}
	if strings.Contains(errLower, "decision_error") || strings.Contains(errLower, "parse error") {
		score -= 0.5
	}
	if strings.Contains(errLower, "asset_symbol is required") || strings.Contains(errLower, "invalid action") {
		score -= 0.4
	}
	if strings.Contains(errLower, "insufficient") {
		score -= 0.2
	}
	if strings.Contains(errLower, "no matching") || strings.Contains(errLower, "liquidity") {
		score -= 0.1
	}
	return score
}

func (r *Runner) refreshBalances(ctx context.Context) {
	if r.Indexer == nil || r.AgentID == "" {
		return
	}
	balCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	balances, err := r.Indexer.GetBalances(balCtx, r.AgentID)
	cancel()
	if err != nil {
		return
	}
	r.lastBalances = balances
}

func (r *Runner) updateTokenPrices(tokens []indexer.Token) {
	if r.lastTokenPrice == nil {
		r.lastTokenPrice = map[string]float64{}
	}
	for _, token := range tokens {
		r.lastTokenPrice[token.Symbol] = token.PriceAGC
	}
}

func (r *Runner) formatHoldings() string {
	if len(r.lastBalances) == 0 {
		return "unknown"
	}
	entries := make([]string, 0, len(r.lastBalances))
	for denom, amount := range r.lastBalances {
		entries = append(entries, fmt.Sprintf("%s %d", denom, amount))
	}
	sort.Strings(entries)
	return strings.Join(entries, ", ")
}

func summarizeOrderbook(tokens []indexer.Token, offers []indexer.Offer, rfqs []indexer.RFQ, selfAgent string, allowedTokens []string) string {
	type marketRow struct {
		symbol  string
		last    float64
		bestAsk float64
		bestBid float64
		score   int
	}
	allowed := map[string]struct{}{}
	for _, token := range allowedTokens {
		symbol := strings.ToUpper(strings.TrimSpace(token))
		if symbol == "" || symbol == "AGC" {
			continue
		}
		allowed[symbol] = struct{}{}
	}
	tokenPrice := map[string]float64{}
	for _, token := range tokens {
		symbol := strings.ToUpper(strings.TrimSpace(token.Symbol))
		if symbol == "" || symbol == "AGC" {
			continue
		}
		if len(allowed) > 0 {
			if _, ok := allowed[symbol]; !ok {
				continue
			}
		}
		tokenPrice[symbol] = token.PriceAGC
	}
	bestAsk := map[string]float64{}
	bestBid := map[string]float64{}
	for _, offer := range offers {
		if strings.TrimSpace(offer.AgentID) == strings.TrimSpace(selfAgent) {
			continue
		}
		if !isOpenStatus(offer.Status) || offer.Qty <= 0 {
			continue
		}
		symbol := strings.ToUpper(strings.TrimSpace(offer.Asset))
		if symbol == "" || symbol == "AGC" {
			continue
		}
		if len(allowed) > 0 {
			if _, ok := allowed[symbol]; !ok {
				continue
			}
		}
		price := offer.PriceAGC
		if current, ok := bestAsk[symbol]; !ok || price < current {
			bestAsk[symbol] = price
		}
	}
	for _, rfq := range rfqs {
		if strings.TrimSpace(rfq.AgentID) == strings.TrimSpace(selfAgent) {
			continue
		}
		if !isOpenStatus(rfq.Status) || rfq.Qty <= 0 {
			continue
		}
		symbol := strings.ToUpper(strings.TrimSpace(rfq.Asset))
		if symbol == "" || symbol == "AGC" {
			continue
		}
		if len(allowed) > 0 {
			if _, ok := allowed[symbol]; !ok {
				continue
			}
		}
		price := rfq.MaxPriceAGC
		if current, ok := bestBid[symbol]; !ok || price > current {
			bestBid[symbol] = price
		}
	}
	symbolSet := map[string]struct{}{}
	for symbol := range tokenPrice {
		symbolSet[symbol] = struct{}{}
	}
	for symbol := range bestAsk {
		symbolSet[symbol] = struct{}{}
	}
	for symbol := range bestBid {
		symbolSet[symbol] = struct{}{}
	}
	if len(symbolSet) == 0 {
		return "no visible liquidity"
	}
	rows := make([]marketRow, 0, len(symbolSet))
	for symbol := range symbolSet {
		row := marketRow{
			symbol:  symbol,
			last:    tokenPrice[symbol],
			bestAsk: bestAsk[symbol],
			bestBid: bestBid[symbol],
			score:   0,
		}
		if row.bestAsk > 0 && row.bestBid > 0 {
			row.score += 3
			if row.bestBid >= row.bestAsk {
				row.score += 3
			}
		}
		if row.last > 0 && row.bestAsk > 0 && row.bestAsk <= row.last*1.03 {
			row.score++
		}
		if row.last > 0 && row.bestBid > 0 && row.bestBid >= row.last*0.97 {
			row.score++
		}
		rows = append(rows, row)
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].score == rows[j].score {
			return rows[i].symbol < rows[j].symbol
		}
		return rows[i].score > rows[j].score
	})
	if len(rows) > 5 {
		rows = rows[:5]
	}
	parts := make([]string, 0, len(rows))
	for _, row := range rows {
		lastText := "n/a"
		if row.last > 0 {
			lastText = fmt.Sprintf("%.2f", row.last)
		}
		askText := "n/a"
		if row.bestAsk > 0 {
			askText = fmt.Sprintf("%.2f", row.bestAsk)
		}
		bidText := "n/a"
		if row.bestBid > 0 {
			bidText = fmt.Sprintf("%.2f", row.bestBid)
		}
		signal := "watch"
		if row.bestBid > 0 && row.bestAsk > 0 && row.bestBid >= row.bestAsk {
			signal = "cross"
		} else if row.bestBid > 0 && row.last > 0 && row.bestBid >= row.last {
			signal = "strong_bid"
		} else if row.bestAsk > 0 && row.last > 0 && row.bestAsk <= row.last {
			signal = "cheap_ask"
		}
		parts = append(parts, fmt.Sprintf("%s last=%s bid=%s ask=%s %s", row.symbol, lastText, bidText, askText, signal))
	}
	return strings.Join(parts, "; ")
}

func trimForPrompt(text string, max int) string {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" || max <= 0 {
		return ""
	}
	if len(trimmed) <= max {
		return trimmed
	}
	return trimmed[:max-3] + "..."
}

func (r *Runner) preflight(action Action) (string, string) {
	if r.lastBalances == nil || len(r.lastBalances) == 0 {
		return "blocked", "balances unavailable"
	}
	asset := strings.ToUpper(strings.TrimSpace(action.AssetSymbol))
	qty := uint64(math.Round(action.Qty))
	if qty == 0 {
		return "blocked", "qty must be positive"
	}
	if asset == "" {
		return "blocked", "asset symbol missing"
	}
	if asset == "AGC" {
		return "blocked", "AGC is settlement asset"
	}

	switch strings.ToLower(strings.TrimSpace(action.Action)) {
	case "post_offer":
		if r.lastOpenOffers >= maxOpenOffersPerAgent {
			return "blocked", "open offer limit reached"
		}
		if r.lastOffersByAS[asset] >= maxOpenOffersPerAsset {
			return "blocked", "asset offer limit reached"
		}
		if action.PriceAGC <= 0 {
			return "blocked", "price must be positive"
		}
		assetBal := r.lastBalances[asset]
		mintQty := uint64(0)
		if assetBal < qty {
			mintQty = qty - assetBal
		}
		needAGC := offerFeeAGC + mintQty*syntheticMintFeePerUnitAGC
		if r.lastBalances["AGC"] < needAGC {
			return "blocked", "insufficient AGC for offer fee/mint"
		}
	case "create_rfq":
		if r.lastOpenRFQs >= maxOpenRFQsPerAgent {
			return "blocked", "open rfq limit reached"
		}
		price := action.PriceAGC
		if price <= 0 {
			price = r.lastTokenPrice[asset]
		}
		if price <= 0 {
			return "blocked", "price unavailable"
		}
		cost := uint64(math.Round(price * float64(qty)))
		if r.lastBalances["AGC"] < cost+rfqFeeAGC {
			return "blocked", "insufficient AGC balance"
		}
	case "trade":
		side := strings.ToLower(strings.TrimSpace(action.Side))
		if side != "buy" && side != "sell" {
			return "blocked", "side must be buy or sell"
		}
		price := action.PriceAGC
		if price <= 0 {
			price = r.lastTokenPrice[asset]
		}
		if price <= 0 {
			return "blocked", "price unavailable"
		}
		cost := uint64(math.Round(price * float64(qty)))
		fee := calcTradeFee(cost)
		if side == "sell" {
			if r.lastBalances[asset] < qty {
				return "blocked", "insufficient asset balance"
			}
			if r.lastBalances["AGC"] < fee {
				return "blocked", "insufficient AGC for fee"
			}
			if !r.hasTradeLiquidity(side, asset, price, qty) {
				return "blocked", "no matching rfq liquidity"
			}
			return "", ""
		}
		if r.lastBalances["AGC"] < cost+fee {
			return "blocked", "insufficient AGC balance"
		}
		if !r.hasTradeLiquidity(side, asset, price, qty) {
			return "blocked", "no matching offer liquidity"
		}
	default:
		return "blocked", "invalid action"
	}
	return "", ""
}

func calcTradeFee(notional uint64) uint64 {
	if tradeFeeBps == 0 || notional == 0 {
		return 0
	}
	fee := (notional * tradeFeeBps) / 10000
	return fee
}

func normalizeAction(action *Action) {
	if action == nil {
		return
	}
	clean := strings.ToLower(strings.TrimSpace(action.Action))
	clean = strings.ReplaceAll(clean, " ", "_")
	clean = strings.ReplaceAll(clean, "-", "_")
	for strings.Contains(clean, "__") {
		clean = strings.ReplaceAll(clean, "__", "_")
	}
	clean = strings.Trim(clean, "_")
	switch clean {
	case "offer", "list", "postoffer", "post_offer", "make_offer":
		clean = "post_offer"
	case "rfq", "create_rfq", "request_quote", "request_rfq", "create_r_fq":
		clean = "create_rfq"
	case "trade", "buy", "sell":
		if clean == "buy" || clean == "sell" {
			action.Side = clean
		}
		clean = "trade"
	case "wait", "hold", "observe", "pause":
		clean = "wait"
	case "noop", "no_op":
		clean = "noop"
	}
	action.Action = clean
	action.AssetSymbol = strings.ToUpper(strings.TrimSpace(action.AssetSymbol))
	action.Side = strings.ToLower(strings.TrimSpace(action.Side))
	action.Category = strings.TrimSpace(action.Category)
	action.Reason = strings.TrimSpace(action.Reason)
	if action.NextCheckSec < 0 {
		action.NextCheckSec = 0
	}
	if action.AssetSymbol == "" {
		return
	}
}

func (r *Runner) hasTradeLiquidity(side, asset string, price float64, qty uint64) bool {
	if qty == 0 {
		return false
	}
	asset = strings.ToUpper(strings.TrimSpace(asset))
	side = strings.ToLower(strings.TrimSpace(side))
	if asset == "" || (side != "buy" && side != "sell") {
		return false
	}
	remaining := float64(qty)
	const eps = 1e-9
	if side == "buy" {
		for _, offer := range r.lastOffers {
			if offer.AgentID == r.AgentID {
				continue
			}
			if !isOpenStatus(offer.Status) {
				continue
			}
			if strings.ToUpper(strings.TrimSpace(offer.Asset)) != asset {
				continue
			}
			if offer.PriceAGC > price+eps {
				continue
			}
			remaining -= offer.Qty
			if remaining <= eps {
				return true
			}
		}
		return false
	}
	for _, rfq := range r.lastRFQs {
		if rfq.AgentID == r.AgentID {
			continue
		}
		if !isOpenStatus(rfq.Status) {
			continue
		}
		if strings.ToUpper(strings.TrimSpace(rfq.Asset)) != asset {
			continue
		}
		if rfq.MaxPriceAGC+eps < price {
			continue
		}
		remaining -= rfq.Qty
		if remaining <= eps {
			return true
		}
	}
	return false
}

func isOpenStatus(status string) bool {
	status = strings.ToLower(strings.TrimSpace(status))
	return status == "" || status == "open"
}
