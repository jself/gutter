package main

import "testing"

func TestParsePRView(t *testing.T) {
	in := []byte(`{
		"number": 123,
		"headRefOid": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		"baseRefOid": "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		"url": "https://github.com/octo/widgets/pull/123"
	}`)
	got, err := parsePRView(in)
	if err != nil {
		t.Fatalf("parsePRView: %v", err)
	}
	want := PRInfo{
		Repo:   "octo/widgets",
		Number: 123,
		Head:   "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Base:   "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
	}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestParsePRViewBadURL(t *testing.T) {
	in := []byte(`{"number":1,"headRefOid":"x","baseRefOid":"y","url":"not-a-url"}`)
	if _, err := parsePRView(in); err == nil {
		t.Fatal("expected error for unparseable url, got nil")
	}
}
