// Package rxnorm is the library behind the rxnorm command line:
// the HTTP client, request shaping, and the typed data models for the
// NLM RxNorm drug terminology API at rxnav.nlm.nih.gov/REST.
//
// The Client here is the spine every command shares. It sets a real
// User-Agent, paces requests so a busy session stays polite, and retries the
// transient failures (429 and 5xx) that any public API throws under load.
package rxnorm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// DefaultUserAgent identifies the client to the NLM API. A real, honest
// User-Agent is both polite and less likely to get blocked.
const DefaultUserAgent = "rxnorm-cli/0.1 (tamnd87@gmail.com)"

// Host is the API host this client talks to.
const Host = "rxnav.nlm.nih.gov"

// BaseURL is the root every request is built from.
const BaseURL = "https://" + Host + "/REST"

// Client talks to the RxNorm REST API over HTTPS.
type Client struct {
	HTTP      *http.Client
	UserAgent string
	// Rate is the minimum gap between requests. Zero means no pacing.
	Rate    time.Duration
	Retries int

	last time.Time
}

// NewClient returns a Client with sensible defaults: a 15s timeout, a 300ms
// minimum gap between requests, and three retries on transient errors.
func NewClient() *Client {
	return &Client{
		HTTP:      &http.Client{Timeout: 15 * time.Second},
		UserAgent: DefaultUserAgent,
		Rate:      300 * time.Millisecond,
		Retries:   3,
	}
}

// Get fetches rawURL and returns the response body. It paces and retries
// according to the client's settings. The caller owns nothing extra; the
// body is read fully and closed here.
func (c *Client) Get(ctx context.Context, rawURL string) ([]byte, error) {
	var lastErr error
	for attempt := 0; attempt <= c.Retries; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(backoff(attempt)):
			}
		}
		body, retry, err := c.do(ctx, rawURL)
		if err == nil {
			return body, nil
		}
		lastErr = err
		if !retry {
			return nil, err
		}
	}
	return nil, fmt.Errorf("get %s: %w", rawURL, lastErr)
}

func (c *Client) do(ctx context.Context, rawURL string) (body []byte, retry bool, err error) {
	c.pace()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, false, err
	}
	req.Header.Set("User-Agent", c.UserAgent)
	req.Header.Set("Accept", "application/json")

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, true, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
		return nil, true, fmt.Errorf("http %d", resp.StatusCode)
	}
	if resp.StatusCode == http.StatusNotFound {
		return nil, false, fmt.Errorf("not found: %s", rawURL)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, false, fmt.Errorf("http %d", resp.StatusCode)
	}

	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, true, err
	}
	return b, false, nil
}

// pace blocks until at least Rate has passed since the previous request.
func (c *Client) pace() {
	if c.Rate <= 0 {
		return
	}
	if wait := c.Rate - time.Since(c.last); wait > 0 {
		time.Sleep(wait)
	}
	c.last = time.Now()
}

func backoff(attempt int) time.Duration {
	d := time.Duration(attempt) * 500 * time.Millisecond
	if d > 5*time.Second {
		d = 5 * time.Second
	}
	return d
}

// --- Data models ---

// Drug is one RxNorm drug concept: a normalized identifier, a canonical
// name, a type derived from the TTY code, and an optional synonym.
type Drug struct {
	RxCUI   string `kit:"id" json:"rxcui"`
	Name    string `json:"name"`
	Type    string `json:"type"`
	Synonym string `json:"synonym,omitempty"`
}

// Interaction records a drug-drug interaction pair with severity and a
// plain-English description.
type Interaction struct {
	Drug1       string `json:"drug1"`
	Drug2       string `json:"drug2"`
	Severity    string `json:"severity"`
	Description string `json:"description"`
}

// unmarshalJSON is a thin wrapper for use by tests and internal callers.
func unmarshalJSON(data []byte, v any) error {
	return json.Unmarshal(data, v)
}

// ttyLabel maps RxNorm term type codes to human-readable labels.
func ttyLabel(tty string) string {
	switch strings.ToUpper(tty) {
	case "IN":
		return "ingredient"
	case "BN":
		return "brand"
	case "SCD":
		return "clinical drug"
	case "SCDF":
		return "clinical drug form"
	case "SCDC":
		return "clinical drug component"
	case "SBDC":
		return "branded drug component"
	case "SBDF":
		return "branded drug form"
	case "SBD":
		return "branded drug"
	case "GPCK":
		return "generic pack"
	case "BPCK":
		return "brand pack"
	case "PIN":
		return "precise ingredient"
	case "MIN":
		return "multiple ingredients"
	default:
		if tty == "" {
			return ""
		}
		return strings.ToLower(tty)
	}
}

// --- Wire types for JSON decoding ---

type rxcuiResponse struct {
	IDGroup struct {
		Name     string   `json:"name"`
		RxNormID []string `json:"rxnormId"`
	} `json:"idGroup"`
}

type propertiesResponse struct {
	Properties struct {
		RxCUI   string `json:"rxcui"`
		Name    string `json:"name"`
		Synonym string `json:"synonym"`
		TTY     string `json:"tty"`
	} `json:"properties"`
}

type drugsResponse struct {
	DrugGroup struct {
		Name         string `json:"name"`
		ConceptGroup []struct {
			TTY               string `json:"tty"`
			ConceptProperties []struct {
				RxCUI   string `json:"rxcui"`
				Name    string `json:"name"`
				Synonym string `json:"synonym"`
				TTY     string `json:"tty"`
			} `json:"conceptProperties"`
		} `json:"conceptGroup"`
	} `json:"drugGroup"`
}

type interactionListResponse struct {
	FullInteractionTypeGroup []struct {
		FullInteractionType []struct {
			InteractionPair []struct {
				InteractionConcept []struct {
					MinConceptItem struct {
						RxCUI string `json:"rxcui"`
						Name  string `json:"name"`
					} `json:"minConceptItem"`
				} `json:"interactionConcept"`
				Severity    string `json:"severity"`
				Description string `json:"description"`
			} `json:"interactionPair"`
		} `json:"fullInteractionType"`
	} `json:"fullInteractionTypeGroup"`
}

// --- Client methods ---

// ResolveName calls /rxcui.json?name={name} and returns all matching RxCUI
// strings. Returns an empty slice (not an error) when the API finds nothing.
func (c *Client) ResolveName(ctx context.Context, name string) ([]string, error) {
	apiURL := BaseURL + "/rxcui.json?name=" + url.QueryEscape(name)
	body, err := c.Get(ctx, apiURL)
	if err != nil {
		return nil, err
	}
	var resp rxcuiResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("parse rxcui response: %w", err)
	}
	return resp.IDGroup.RxNormID, nil
}

// DrugProperties calls /rxcui/{id}/properties.json and returns the canonical
// name and TTY for the given RxCUI.
func (c *Client) DrugProperties(ctx context.Context, rxcui string) (name, tty string, err error) {
	apiURL := BaseURL + "/rxcui/" + rxcui + "/properties.json"
	body, err := c.Get(ctx, apiURL)
	if err != nil {
		return "", "", err
	}
	var resp propertiesResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return "", "", fmt.Errorf("parse properties response: %w", err)
	}
	return resp.Properties.Name, resp.Properties.TTY, nil
}

// Lookup resolves a drug name to one or more Drug records, fetching
// properties for each matching RxCUI. Returns an error if nothing is found.
func (c *Client) Lookup(ctx context.Context, drugName string) ([]*Drug, error) {
	ids, err := c.ResolveName(ctx, drugName)
	if err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return nil, fmt.Errorf("no drug found for %q", drugName)
	}
	var drugs []*Drug
	for _, id := range ids {
		name, tty, err := c.DrugProperties(ctx, id)
		if err != nil {
			return nil, err
		}
		if name == "" {
			name = drugName
		}
		drugs = append(drugs, &Drug{
			RxCUI: id,
			Name:  name,
			Type:  ttyLabel(tty),
		})
	}
	return drugs, nil
}

// Drugs searches for drugs by name using /drugs.json and returns one Drug
// record per concept in every concept group. Returns an error if nothing
// is found.
func (c *Client) Drugs(ctx context.Context, drugName string) ([]*Drug, error) {
	apiURL := BaseURL + "/drugs.json?name=" + url.QueryEscape(drugName)
	body, err := c.Get(ctx, apiURL)
	if err != nil {
		return nil, err
	}
	var resp drugsResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("parse drugs response: %w", err)
	}
	var drugs []*Drug
	for _, group := range resp.DrugGroup.ConceptGroup {
		for _, cp := range group.ConceptProperties {
			tty := cp.TTY
			if tty == "" {
				tty = group.TTY
			}
			drugs = append(drugs, &Drug{
				RxCUI:   cp.RxCUI,
				Name:    cp.Name,
				Type:    ttyLabel(tty),
				Synonym: cp.Synonym,
			})
		}
	}
	if len(drugs) == 0 {
		return nil, fmt.Errorf("no drugs found for %q", drugName)
	}
	return drugs, nil
}

// Interactions resolves each name to a RxCUI, then calls
// /interaction/list.json to find all drug-drug interactions among them.
// At least 2 names must resolve; returns an error otherwise.
func (c *Client) Interactions(ctx context.Context, names []string) ([]*Interaction, error) {
	// Resolve each name to RxCUI, dedup.
	seen := map[string]bool{}
	var rxcuis []string
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		ids, err := c.ResolveName(ctx, name)
		if err != nil {
			return nil, fmt.Errorf("resolve %q: %w", name, err)
		}
		if len(ids) == 0 {
			continue
		}
		id := ids[0] // use the first (best) match
		if !seen[id] {
			seen[id] = true
			rxcuis = append(rxcuis, id)
		}
	}
	if len(rxcuis) < 2 {
		return nil, fmt.Errorf("need at least 2 drug names that resolve to RxCUI values")
	}

	apiURL := BaseURL + "/interaction/list.json?rxcuis=" + strings.Join(rxcuis, "+")
	body, err := c.Get(ctx, apiURL)
	if err != nil {
		return nil, err
	}
	var resp interactionListResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("parse interaction response: %w", err)
	}

	var out []*Interaction
	for _, tg := range resp.FullInteractionTypeGroup {
		for _, it := range tg.FullInteractionType {
			for _, pair := range it.InteractionPair {
				if len(pair.InteractionConcept) < 2 {
					continue
				}
				out = append(out, &Interaction{
					Drug1:       pair.InteractionConcept[0].MinConceptItem.Name,
					Drug2:       pair.InteractionConcept[1].MinConceptItem.Name,
					Severity:    pair.Severity,
					Description: pair.Description,
				})
			}
		}
	}
	return out, nil
}
