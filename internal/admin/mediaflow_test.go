package admin

import (
	"github.com/braedonsaunders/ether/internal/config"
	"testing"
)

func TestMediaFlowPasswordRedacted(t *testing.T) {
	var c config.Config
	c.Outbound.AllDebridMediaFlow = config.MediaFlow{Enabled: true, URL: "http://relay:8888", Password: "secret"}
	out := RedactConfig(c)
	if out.Outbound.AllDebridMediaFlow.Password == "secret" || out.Outbound.AllDebridMediaFlow.Password == "" {
		t.Fatal("password must be masked")
	}
	if c.Outbound.AllDebridMediaFlow.Password != "secret" {
		t.Fatal("original changed")
	}
}
