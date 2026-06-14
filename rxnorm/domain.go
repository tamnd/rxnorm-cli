package rxnorm

import (
	"context"
	"strings"

	"github.com/tamnd/any-cli/kit"
	"github.com/tamnd/any-cli/kit/errs"
)

// domain.go exposes the RxNorm library as a kit Domain: a driver that a
// multi-domain host (ant) enables with a single blank import,
//
//	import _ "github.com/tamnd/rxnorm-cli/rxnorm"
//
// exactly as a database/sql program enables a driver with `import _
// "github.com/lib/pq"`. The init below registers it; the host then
// dereferences rxnorm:// URIs by routing to the operations Register installs.
// The same Domain also builds the standalone rxnorm binary, so the binary
// and a host share one source of truth.
func init() { kit.Register(Domain{}) }

// Domain is the rxnorm driver. It carries no state; the per-run client is
// built by the factory Register hands kit.
type Domain struct{}

// Info describes the scheme, the hostnames a pasted link is matched against,
// and the identity reused for the binary's help and version.
func (Domain) Info() kit.DomainInfo {
	return kit.DomainInfo{
		Scheme: "rxnorm",
		Hosts:  []string{Host},
		Identity: kit.Identity{
			Binary: "rxnorm",
			Short:  "A command line for the RxNorm drug terminology API.",
			Long: `A command line for the RxNorm drug terminology API.

rxnorm reads public NLM RxNorm data over plain HTTPS, shapes it into
clean records, and prints output that pipes into the rest of your tools. No API
key, nothing to run alongside it.`,
			Site: Host,
			Repo: "https://github.com/tamnd/rxnorm-cli",
		},
	}
}

// Register installs the client factory and every operation onto app.
func (Domain) Register(app *kit.App) {
	app.SetClient(newClient)

	// lookup: resolve a drug name to its RxCUI and canonical properties.
	kit.Handle(app, kit.OpMeta{
		Name:    "lookup",
		Group:   "read",
		Summary: "Look up a drug by name and return its RxCUI and type",
		Args:    []kit.Arg{{Name: "name", Help: "drug name (e.g. aspirin)"}},
	}, lookupDrug)

	// drugs: search for drugs by name, returning all matching concept groups.
	kit.Handle(app, kit.OpMeta{
		Name:    "drugs",
		Group:   "read",
		Summary: "Search for drugs by name",
		Args:    []kit.Arg{{Name: "name", Help: "drug name to search for"}},
	}, searchDrugs)

	// interactions: check drug-drug interactions for two or more drugs.
	kit.Handle(app, kit.OpMeta{
		Name:    "interactions",
		Group:   "read",
		Summary: "Check drug-drug interactions",
		Args:    []kit.Arg{{Name: "names", Help: "drug names separated by spaces (e.g. warfarin aspirin)", Variadic: true}},
	}, checkInteractions)
}

// newClient builds the client from the host-resolved config, so a host and the
// standalone binary pace and identify themselves the same way.
func newClient(_ context.Context, cfg kit.Config) (any, error) {
	c := NewClient()
	if cfg.UserAgent != "" {
		c.UserAgent = cfg.UserAgent
	}
	if cfg.Rate > 0 {
		c.Rate = cfg.Rate
	}
	if cfg.Retries > 0 {
		c.Retries = cfg.Retries
	}
	if cfg.Timeout > 0 {
		c.HTTP.Timeout = cfg.Timeout
	}
	return c, nil
}

// --- inputs ---

type lookupInput struct {
	Name   string  `kit:"arg" help:"drug name (e.g. aspirin)"`
	Client *Client `kit:"inject"`
}

type drugsInput struct {
	Name   string  `kit:"arg" help:"drug name to search for"`
	Limit  int     `kit:"flag,inherit" help:"max results"`
	Client *Client `kit:"inject"`
}

type interactionsInput struct {
	Names  []string `kit:"arg,variadic" help:"drug names (2 or more)"`
	Client *Client  `kit:"inject"`
}

// --- handlers ---

func lookupDrug(ctx context.Context, in lookupInput, emit func(*Drug) error) error {
	drugs, err := in.Client.Lookup(ctx, in.Name)
	if err != nil {
		if strings.Contains(err.Error(), "no drug found") {
			return errs.NotFound("%s", err.Error())
		}
		return err
	}
	for _, d := range drugs {
		if err := emit(d); err != nil {
			return err
		}
	}
	return nil
}

func searchDrugs(ctx context.Context, in drugsInput, emit func(*Drug) error) error {
	drugs, err := in.Client.Drugs(ctx, in.Name)
	if err != nil {
		if strings.Contains(err.Error(), "no drugs found") {
			return errs.NotFound("%s", err.Error())
		}
		return err
	}
	for i, d := range drugs {
		if in.Limit > 0 && i >= in.Limit {
			break
		}
		if err := emit(d); err != nil {
			return err
		}
	}
	return nil
}

func checkInteractions(ctx context.Context, in interactionsInput, emit func(*Interaction) error) error {
	if len(in.Names) < 2 {
		return errs.Usage("interactions requires at least 2 drug names")
	}
	interactions, err := in.Client.Interactions(ctx, in.Names)
	if err != nil {
		if strings.Contains(err.Error(), "at least 2") {
			return errs.Usage("%s", err.Error())
		}
		if strings.Contains(err.Error(), "not found") {
			return errs.NotFound("interaction data unavailable (the NLM interaction API may be temporarily down)")
		}
		return err
	}
	for _, inter := range interactions {
		if err := emit(inter); err != nil {
			return err
		}
	}
	return nil
}

// --- Resolver: URI string functions, pure and network-free ---

// Classify turns an RxCUI id or a full URL into the canonical (type, id).
func (Domain) Classify(input string) (uriType, id string, err error) {
	input = strings.TrimSpace(input)
	if input == "" {
		return "", "", errs.Usage("empty rxnorm reference")
	}
	// Strip scheme and host if a full URL was pasted.
	if strings.HasPrefix(input, "http://") || strings.HasPrefix(input, "https://") {
		// Extract the last path segment as the rxcui.
		parts := strings.Split(strings.Trim(input, "/"), "/")
		input = parts[len(parts)-1]
	}
	input = strings.Trim(input, "/")
	if input == "" {
		return "", "", errs.Usage("unrecognized rxnorm reference")
	}
	return "drug", input, nil
}

// Locate is the inverse: the live https URL for a (type, id).
func (Domain) Locate(uriType, id string) (string, error) {
	if uriType != "drug" {
		return "", errs.Usage("rxnorm has no resource type %q", uriType)
	}
	return "https://" + Host + "/REST/rxcui/" + id + "/allProperties.json?prop=names", nil
}
