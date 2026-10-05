package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDraftRoundTrip(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "review.md")
	p := draftPathFor(out)
	if p != out+".draft.json" {
		t.Fatalf("path = %q", p)
	}
	d := Draft{Key: "@", Comments: []Comment{{Path: "a.go", Side: "new", Line: 3, Body: "hi"}}, General: "g"}
	if err := writeDraft(p, d); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p + ".tmp"); err == nil {
		t.Error("tmp file left behind")
	}
	got := loadDraft(p, "@")
	if got == nil || len(got.Comments) != 1 || got.Comments[0].Body != "hi" || got.General != "g" {
		t.Fatalf("got = %+v", got)
	}
	if loadDraft(p, "main..@") != nil {
		t.Error("draft for another key should be ignored")
	}
	if loadDraft(filepath.Join(dir, "missing"), "@") != nil {
		t.Error("missing draft should be nil")
	}
}

func TestDraftEmptyIsNil(t *testing.T) {
	p := filepath.Join(t.TempDir(), "r.md.draft.json")
	if err := writeDraft(p, Draft{Key: "@", General: "  "}); err != nil {
		t.Fatal(err)
	}
	if loadDraft(p, "@") != nil {
		t.Error("empty draft should be nil")
	}
}
