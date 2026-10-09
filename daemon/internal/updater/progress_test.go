package updater

import "testing"

func TestUpdateResultRoundTrip(t *testing.T) {
	dir := t.TempDir()
	if ReadUpdateResult(dir) != nil {
		t.Fatal("no result should exist yet")
	}
	if err := WriteUpdateResult(dir, UpdateResult{Status: "rolled-back", From: "1.0.0", To: "1.1.0", Message: "went back"}); err != nil {
		t.Fatal(err)
	}
	result := ReadUpdateResult(dir)
	if result == nil || result.Status != "rolled-back" || result.To != "1.1.0" || result.At == "" {
		t.Fatalf("unexpected result: %#v", result)
	}
	ClearUpdateResult(dir)
	if ReadUpdateResult(dir) != nil {
		t.Fatal("dismissing must clear the result")
	}
}
