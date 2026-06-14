package rxnorm

import (
	"testing"

	"github.com/tamnd/any-cli/kit"
)

// These tests are offline: they exercise the URI driver's pure string
// functions and the host wiring (classify, locate, resolve), which need
// no network.

func TestDomainInfo(t *testing.T) {
	info := Domain{}.Info()
	if info.Scheme != "rxnorm" {
		t.Errorf("Scheme = %q, want rxnorm", info.Scheme)
	}
	if len(info.Hosts) == 0 || info.Hosts[0] != Host {
		t.Errorf("Hosts = %v, want [%s]", info.Hosts, Host)
	}
	if info.Identity.Binary != "rxnorm" {
		t.Errorf("Identity.Binary = %q, want rxnorm", info.Identity.Binary)
	}
}

func TestClassify(t *testing.T) {
	cases := []struct {
		in, typ, id string
	}{
		{"1191", "drug", "1191"},
		{"/1191/", "drug", "1191"},
		{"https://" + Host + "/REST/rxcui/1191", "drug", "1191"},
	}
	for _, tc := range cases {
		typ, id, err := Domain{}.Classify(tc.in)
		if err != nil || typ != tc.typ || id != tc.id {
			t.Errorf("Classify(%q) = (%q, %q, %v), want (%q, %q, nil)",
				tc.in, typ, id, err, tc.typ, tc.id)
		}
	}
}

func TestClassifyEmpty(t *testing.T) {
	_, _, err := Domain{}.Classify("")
	if err == nil {
		t.Error("Classify(\"\") should return an error")
	}
}

func TestLocate(t *testing.T) {
	got, err := Domain{}.Locate("drug", "1191")
	if err != nil {
		t.Fatalf("Locate: %v", err)
	}
	want := "https://" + Host + "/REST/rxcui/1191/allProperties.json?prop=names"
	if got != want {
		t.Errorf("Locate = %q, want %q", got, want)
	}
}

func TestLocateUnknownType(t *testing.T) {
	_, err := Domain{}.Locate("unknown", "1191")
	if err == nil {
		t.Error("Locate with unknown type should return an error")
	}
}

// TestHostWiring mounts the driver in a kit Host and checks the round trip:
// a record mints to its URI, and ResolveOn works as expected. The init in
// domain.go registers the domain, so kit.Open finds it.
func TestHostWiring(t *testing.T) {
	h, err := kit.Open()
	if err != nil {
		t.Fatal(err)
	}

	got, err := h.ResolveOn("rxnorm", "1191")
	if err != nil {
		t.Fatalf("ResolveOn: %v", err)
	}
	if got.String() != "rxnorm://drug/1191" {
		t.Errorf("ResolveOn = %q, want rxnorm://drug/1191", got.String())
	}
}
