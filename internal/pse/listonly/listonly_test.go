package listonly

import (
	"context"
	"testing"
)

func TestWithoutRemovesMark(t *testing.T) {
	ctx := With(context.Background())
	if !On(ctx) || On(Without(ctx)) {
		t.Fatal("Without doit retirer la marque de liste légère")
	}
	if bg := context.Background(); Without(bg) != bg {
		t.Fatal("contexte sans marque : inchangé")
	}
}
