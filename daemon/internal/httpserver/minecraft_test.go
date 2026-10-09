package httpserver

import (
	"testing"
)

func TestPaperServerTypeDoesNotUseLoaderVersions(t *testing.T) {
	if !validServerType("paper") {
		t.Fatal("paper should be a valid server type")
	}
	if serverTypeNeedsLoader("paper") {
		t.Fatal("paper should not use Fabric, Forge, or NeoForge loader versions")
	}
}
