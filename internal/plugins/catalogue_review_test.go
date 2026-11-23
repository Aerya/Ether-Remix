package plugins

import "testing"

// A catalogue entry cannot declare a plugin's libraries, so a content plugin
// whose pinned package adds one must still install; an undeclared network
// host must still be refused.
func TestCatalogueReviewAcceptsPinnedContributions(t *testing.T) {
	man := Manifest{Ether: &EtherBlock{
		Kind:         "content",
		Capabilities: []Capability{"catalog_provider"},
		Permissions:  Permissions{Network: []string{"api.example.org"}},
		Contributes:  Contributes{MediaKinds: []MediaKindMeta{{Key: "music", Library: KindLibraryDecl{Name: "Music"}}}},
	}}
	reviewed := CatalogueArtifact{Kind: "content", Capabilities: []Capability{"catalog_provider"}, Permissions: Permissions{Network: []string{"api.example.org"}}}
	if added := catalogueReviewDiff(reviewed, man); len(added) != 0 {
		t.Fatalf("pinned library treated as unreviewed: %v", added)
	}
	reviewed.Permissions.Network = nil
	if added := catalogueReviewDiff(reviewed, man); len(added) == 0 {
		t.Fatal("undeclared network host accepted")
	}
}
