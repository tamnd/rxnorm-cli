package rxnorm

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestGet(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("User-Agent") == "" {
			t.Error("request carried no User-Agent")
		}
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	c := NewClient()
	c.Rate = 0 // no pacing in the test

	body, err := c.Get(context.Background(), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "ok" {
		t.Errorf("body = %q, want %q", body, "ok")
	}
}

func TestGetRetriesOn503(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if hits < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte("recovered"))
	}))
	defer srv.Close()

	c := NewClient()
	c.Rate = 0
	c.Retries = 5

	start := time.Now()
	body, err := c.Get(context.Background(), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "recovered" {
		t.Errorf("body = %q after retries", body)
	}
	if hits != 3 {
		t.Errorf("server saw %d hits, want 3", hits)
	}
	if time.Since(start) < 500*time.Millisecond {
		t.Error("retries did not back off")
	}
}

func TestLookup(t *testing.T) {
	// The test server handles both /rxcui.json and /rxcui/1191/allProperties.json
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/rxcui.json"):
			_, _ = w.Write([]byte(`{"idGroup":{"name":"aspirin","rxnormId":["1191"]}}`))
		case strings.Contains(r.URL.Path, "/rxcui/1191/properties.json"):
			_, _ = w.Write([]byte(`{"properties":{"rxcui":"1191","name":"aspirin","synonym":"","tty":"IN","language":"ENG","suppress":"N"}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	c := NewClient()
	c.Rate = 0

	// Test ResolveName
	ids, err := resolveNameURL(context.Background(), c, srv.URL+"/rxcui.json?name=aspirin")
	if err != nil {
		t.Fatalf("ResolveName: %v", err)
	}
	if len(ids) != 1 || ids[0] != "1191" {
		t.Errorf("ResolveName = %v, want [1191]", ids)
	}

	// Test DrugProperties
	name, tty, err := drugPropertiesURL(context.Background(), c, srv.URL+"/rxcui/1191/properties.json")
	if err != nil {
		t.Fatalf("DrugProperties: %v", err)
	}
	if name != "aspirin" {
		t.Errorf("name = %q, want aspirin", name)
	}
	if tty != "IN" {
		t.Errorf("tty = %q, want IN", tty)
	}
}

func TestDrugs(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"drugGroup": {
				"name": "metformin",
				"conceptGroup": [
					{
						"tty": "IN",
						"conceptProperties": [
							{"rxcui":"6809","name":"metformin","synonym":"","tty":"IN","language":"ENG","suppress":"N"}
						]
					},
					{
						"tty": "BN",
						"conceptProperties": [
							{"rxcui":"861007","name":"Glucophage","synonym":"metformin","tty":"BN","language":"ENG","suppress":"N"}
						]
					}
				]
			}
		}`))
	}))
	defer srv.Close()

	c := NewClient()
	c.Rate = 0

	drugs, err := drugsURL(context.Background(), c, srv.URL+"/drugs.json?name=metformin")
	if err != nil {
		t.Fatalf("Drugs: %v", err)
	}
	if len(drugs) != 2 {
		t.Fatalf("got %d drugs, want 2", len(drugs))
	}
	if drugs[0].RxCUI != "6809" || drugs[0].Type != "ingredient" {
		t.Errorf("drugs[0] = %+v, want rxcui=6809 type=ingredient", drugs[0])
	}
	if drugs[1].RxCUI != "861007" || drugs[1].Type != "brand" {
		t.Errorf("drugs[1] = %+v, want rxcui=861007 type=brand", drugs[1])
	}
}

func TestInteractions(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"fullInteractionTypeGroup": [
				{
					"fullInteractionType": [
						{
							"interactionPair": [
								{
									"interactionConcept": [
										{"minConceptItem": {"rxcui":"1191","name":"aspirin"}},
										{"minConceptItem": {"rxcui":"207106","name":"warfarin"}}
									],
									"severity": "high",
									"description": "Warfarin and Aspirin interact with bleeding risk."
								}
							]
						}
					]
				}
			]
		}`))
	}))
	defer srv.Close()

	c := NewClient()
	c.Rate = 0

	interactions, err := interactionsURL(context.Background(), c, srv.URL+"/interaction/list.json?rxcuis=1191+207106")
	if err != nil {
		t.Fatalf("Interactions: %v", err)
	}
	if len(interactions) != 1 {
		t.Fatalf("got %d interactions, want 1", len(interactions))
	}
	ix := interactions[0]
	if ix.Drug1 != "aspirin" || ix.Drug2 != "warfarin" {
		t.Errorf("drug pair = (%q, %q), want (aspirin, warfarin)", ix.Drug1, ix.Drug2)
	}
	if ix.Severity != "high" {
		t.Errorf("severity = %q, want high", ix.Severity)
	}
	if ix.Description == "" {
		t.Error("description is empty")
	}
}

func TestTTYLabel(t *testing.T) {
	cases := []struct{ tty, want string }{
		{"IN", "ingredient"},
		{"BN", "brand"},
		{"SCD", "clinical drug"},
		{"SCDF", "clinical drug form"},
		{"SBD", "branded drug"},
		{"GPCK", "generic pack"},
		{"BPCK", "brand pack"},
		{"PIN", "precise ingredient"},
		{"MIN", "multiple ingredients"},
		{"XYZ", "xyz"},
		{"", ""},
	}
	for _, tc := range cases {
		got := ttyLabel(tc.tty)
		if got != tc.want {
			t.Errorf("ttyLabel(%q) = %q, want %q", tc.tty, got, tc.want)
		}
	}
}

// --- test helpers that call JSON parsing against a given URL ---
// These mirror the internal logic of ResolveName/DrugProperties/Drugs/Interactions
// but accept a custom URL so we can point them at httptest servers.

func resolveNameURL(ctx context.Context, c *Client, apiURL string) ([]string, error) {
	body, err := c.Get(ctx, apiURL)
	if err != nil {
		return nil, err
	}
	var resp rxcuiResponse
	if err := unmarshalJSON(body, &resp); err != nil {
		return nil, err
	}
	return resp.IDGroup.RxNormID, nil
}

func drugPropertiesURL(ctx context.Context, c *Client, apiURL string) (name, tty string, err error) {
	body, err := c.Get(ctx, apiURL)
	if err != nil {
		return "", "", err
	}
	var resp propertiesResponse
	if err := unmarshalJSON(body, &resp); err != nil {
		return "", "", err
	}
	return resp.Properties.Name, resp.Properties.TTY, nil
}

func drugsURL(ctx context.Context, c *Client, apiURL string) ([]*Drug, error) {
	body, err := c.Get(ctx, apiURL)
	if err != nil {
		return nil, err
	}
	var resp drugsResponse
	if err := unmarshalJSON(body, &resp); err != nil {
		return nil, err
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
	return drugs, nil
}

func interactionsURL(ctx context.Context, c *Client, apiURL string) ([]*Interaction, error) {
	body, err := c.Get(ctx, apiURL)
	if err != nil {
		return nil, err
	}
	var resp interactionListResponse
	if err := unmarshalJSON(body, &resp); err != nil {
		return nil, err
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
