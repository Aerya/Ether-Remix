package torrentclient

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// XML-RPC minimal pour rTorrent : chaînes, entiers et tableaux en entrée ;
// chaînes, entiers, booléens, réels, tableaux et structures en sortie.

func xmlrpcRequest(method string, params ...any) ([]byte, error) {
	var b bytes.Buffer
	b.WriteString(`<?xml version="1.0"?><methodCall><methodName>`)
	xml.EscapeText(&b, []byte(method))
	b.WriteString(`</methodName><params>`)
	for _, p := range params {
		b.WriteString("<param>")
		if err := xmlrpcValue(&b, p); err != nil {
			return nil, err
		}
		b.WriteString("</param>")
	}
	b.WriteString(`</params></methodCall>`)
	return b.Bytes(), nil
}

func xmlrpcValue(b *bytes.Buffer, v any) error {
	b.WriteString("<value>")
	switch x := v.(type) {
	case string:
		b.WriteString("<string>")
		xml.EscapeText(b, []byte(x))
		b.WriteString("</string>")
	case int:
		b.WriteString("<i8>" + strconv.Itoa(x) + "</i8>")
	case int64:
		b.WriteString("<i8>" + strconv.FormatInt(x, 10) + "</i8>")
	case []any:
		b.WriteString("<array><data>")
		for _, e := range x {
			if err := xmlrpcValue(b, e); err != nil {
				return err
			}
		}
		b.WriteString("</data></array>")
	default:
		return fmt.Errorf("xmlrpc: type %T non pris en charge", v)
	}
	b.WriteString("</value>")
	return nil
}

// errFault est une erreur renvoyée par rTorrent (torrent inconnu, etc.).
type errFault struct {
	code int64
	msg  string
}

func (e *errFault) Error() string { return fmt.Sprintf("rtorrent: erreur %d : %s", e.code, e.msg) }

// xmlrpcResponse décode la valeur renvoyée.
func xmlrpcResponse(r io.Reader) (any, error) {
	d := xml.NewDecoder(r)
	d.Strict = false
	inFault := false
	for {
		tok, err := d.Token()
		if err != nil {
			return nil, errors.New("xmlrpc: réponse illisible")
		}
		se, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		switch se.Name.Local {
		case "fault":
			inFault = true
		case "value":
			v, err := decodeValue(d, 0)
			if err != nil {
				return nil, err
			}
			if inFault {
				m, _ := v.(map[string]any)
				code, _ := m["faultCode"].(int64)
				msg, _ := m["faultString"].(string)
				return nil, &errFault{code: code, msg: msg}
			}
			return v, nil
		}
	}
}

// decodeValue lit le contenu d'un <value> déjà ouvert, jusqu'à sa fermeture.
func decodeValue(d *xml.Decoder, depth int) (any, error) {
	if depth > 32 {
		return nil, errors.New("xmlrpc: réponse trop imbriquée")
	}
	var text strings.Builder
	var out any
	typed := false
	for {
		tok, err := d.Token()
		if err != nil {
			return nil, errors.New("xmlrpc: réponse tronquée")
		}
		switch t := tok.(type) {
		case xml.CharData:
			if !typed {
				text.Write(t)
			}
		case xml.EndElement:
			if t.Name.Local == "value" {
				if !typed {
					return text.String(), nil
				}
				return out, nil
			}
		case xml.StartElement:
			typed = true
			switch t.Name.Local {
			case "string", "base64":
				s, err := readText(d)
				if err != nil {
					return nil, err
				}
				out = s
			case "i4", "i8", "int":
				s, err := readText(d)
				if err != nil {
					return nil, err
				}
				n, _ := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
				out = n
			case "boolean":
				s, err := readText(d)
				if err != nil {
					return nil, err
				}
				out = strings.TrimSpace(s) == "1"
			case "double":
				s, err := readText(d)
				if err != nil {
					return nil, err
				}
				f, _ := strconv.ParseFloat(strings.TrimSpace(s), 64)
				out = f
			case "array":
				arr, err := decodeArray(d, depth)
				if err != nil {
					return nil, err
				}
				out = arr
			case "struct":
				m, err := decodeStruct(d, depth)
				if err != nil {
					return nil, err
				}
				out = m
			default:
				if err := d.Skip(); err != nil {
					return nil, err
				}
			}
		}
	}
}

func readText(d *xml.Decoder) (string, error) {
	var b strings.Builder
	for {
		tok, err := d.Token()
		if err != nil {
			return "", errors.New("xmlrpc: réponse tronquée")
		}
		switch t := tok.(type) {
		case xml.CharData:
			b.Write(t)
		case xml.EndElement:
			return b.String(), nil
		}
	}
}

func decodeArray(d *xml.Decoder, depth int) ([]any, error) {
	var out []any
	for {
		tok, err := d.Token()
		if err != nil {
			return nil, errors.New("xmlrpc: réponse tronquée")
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if t.Name.Local == "value" {
				v, err := decodeValue(d, depth+1)
				if err != nil {
					return nil, err
				}
				out = append(out, v)
			}
		case xml.EndElement:
			if t.Name.Local == "array" {
				return out, nil
			}
		}
	}
}

func decodeStruct(d *xml.Decoder, depth int) (map[string]any, error) {
	out := map[string]any{}
	name := ""
	for {
		tok, err := d.Token()
		if err != nil {
			return nil, errors.New("xmlrpc: réponse tronquée")
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "name":
				s, err := readText(d)
				if err != nil {
					return nil, err
				}
				name = s
			case "value":
				v, err := decodeValue(d, depth+1)
				if err != nil {
					return nil, err
				}
				out[name] = v
			}
		case xml.EndElement:
			if t.Name.Local == "struct" {
				return out, nil
			}
		}
	}
}
