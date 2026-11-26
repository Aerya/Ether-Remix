package screendoc

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"sync"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/braedonsaunders/ether/schemas"
)

// Vela's generated schema checks the node language (blocks, styles,
// conditions, actions) that Ether does not interpret. It validates the
// document as served, with Ether's fields filled in, so a document that
// installs is one Vela's renderer accepts.

var (
	schemaOnce sync.Once
	schemaDoc  *jsonschema.Schema
	schemaErr  error
)

const schemaURI = "https://vela.app/schemas/plugin-screen.v1.json"

func screenSchema() (*jsonschema.Schema, error) {
	schemaOnce.Do(func() {
		sum := sha256.Sum256([]byte(schemas.PluginScreen))
		if hex.EncodeToString(sum[:]) != schemas.PluginScreenDigest {
			schemaErr = errors.New("screendoc: vendored plugin-screen schema does not match its digest")
			return
		}
		doc, err := jsonschema.UnmarshalJSON(strings.NewReader(schemas.PluginScreen))
		if err != nil {
			schemaErr = err
			return
		}
		c := jsonschema.NewCompiler()
		if err := c.AddResource(schemaURI, doc); err != nil {
			schemaErr = err
			return
		}
		schemaDoc, schemaErr = c.Compile(schemaURI)
	})
	return schemaDoc, schemaErr
}

// checkSchema validates a served document against Vela's schema and turns
// its errors into field errors.
func (c *compiler) checkSchema(served []byte) {
	sch, err := screenSchema()
	if err != nil {
		c.fail("", "%v", err)
		return
	}
	v, err := jsonschema.UnmarshalJSON(bytes.NewReader(served))
	if err != nil {
		c.fail("", "%v", err)
		return
	}
	err = sch.Validate(v)
	var ve *jsonschema.ValidationError
	if !errors.As(err, &ve) {
		if err != nil {
			c.fail("", "%v", err)
		}
		return
	}
	for _, leaf := range leaves(ve) {
		ptr := "/" + strings.Join(leaf.InstanceLocation, "/")
		c.fail(ptr, "%s", leafMessage(leaf))
	}
}

func leaves(ve *jsonschema.ValidationError) []*jsonschema.ValidationError {
	if len(ve.Causes) == 0 {
		return []*jsonschema.ValidationError{ve}
	}
	var out []*jsonschema.ValidationError
	for _, c := range ve.Causes {
		out = append(out, leaves(c)...)
		if len(out) >= 10 {
			break
		}
	}
	return out
}

func leafMessage(ve *jsonschema.ValidationError) string {
	msg := ve.Error()
	// "jsonschema validation failed with '...'\n- at '/x': reason" -> reason
	if i := strings.LastIndex(msg, "': "); i >= 0 {
		msg = msg[i+3:]
	}
	return "Vela's document schema: " + strings.TrimSpace(msg)
}
