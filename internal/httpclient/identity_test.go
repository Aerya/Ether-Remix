package httpclient_test

import (
	"testing"

	"github.com/braedonsaunders/ether/internal/httpclient"
	"github.com/braedonsaunders/ether/internal/updater"
)

func TestUserAgentUsesBuildVersion(t *testing.T) {
	prev := updater.Version
	t.Cleanup(func() { updater.Version = prev })

	updater.Version = "dev"
	if got, want := httpclient.UserAgent(), "Ether/dev (+https://github.com/braedonsaunders/ether)"; got != want {
		t.Fatalf("dev UserAgent() = %q, want %q", got, want)
	}

	updater.Version = "0.3.15"
	if got, want := httpclient.UserAgent(), "Ether/0.3.15 (+https://github.com/braedonsaunders/ether)"; got != want {
		t.Fatalf("release UserAgent() = %q, want %q", got, want)
	}

	updater.Version = "   "
	if got, want := httpclient.UserAgent(), "Ether/dev (+https://github.com/braedonsaunders/ether)"; got != want {
		t.Fatalf("blank UserAgent() = %q, want %q", got, want)
	}

	updater.Version = "0.3.17"
	if got, want := httpclient.IndexerUserAgent(), "Ether/0.3.17 (+https://github.com/braedonsaunders/ether/blob/main/docs/indexer-operators.md)"; got != want {
		t.Fatalf("release IndexerUserAgent() = %q, want %q", got, want)
	}

	updater.Version = "   "
	if got, want := httpclient.IndexerUserAgent(), "Ether/dev (+https://github.com/braedonsaunders/ether/blob/main/docs/indexer-operators.md)"; got != want {
		t.Fatalf("blank IndexerUserAgent() = %q, want %q", got, want)
	}
}
