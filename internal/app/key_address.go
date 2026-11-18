package app

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"

	"github.com/fxamacker/cbor/v2"
)

// The saved-key address is ConnInfo encoded in tailcat v0.7's compact CBOR
// format. It needs no listener, DERP lookup, or private-key mutation.
type addressPreview struct {
	Address     string `json:"address,omitempty"`
	AddressMode string `json:"address_mode,omitempty"`
	AddressNote string `json:"address_note,omitempty"`
}
type addressPayload struct {
	Node   []byte         `cbor:"p"`
	Disco  []byte         `cbor:"k,omitempty"`
	PSK    []byte         `cbor:"q,omitempty"`
	Relays []addressRelay `cbor:"r,omitempty"`
	Region int64          `cbor:"i,omitempty"`
}
type addressRelay struct {
	Nodes []addressRelayNode `cbor:"N,omitempty"`
}
type addressRelayNode struct {
	Name        string `cbor:"n,omitempty"`
	Host        string `cbor:"h,omitempty"`
	Certificate string `cbor:"t,omitempty"`
	IPv4        string `cbor:"4,omitempty"`
	IPv6        string `cbor:"6,omitempty"`
	STUNPort    int    `cbor:"s,omitempty"`
	DERPPort    int    `cbor:"d,omitempty"`
	Insecure    bool   `cbor:"x,omitempty"`
}
type savedAddressNode struct {
	Name, HostName, CertName, IPv4, IPv6 string
	STUNPort, DERPPort                   int
	InsecureForTests, STUNOnly           bool
}
type savedAddressRelay struct{ Nodes []*savedAddressNode }
type savedAddressMetadata struct {
	ServerPublic, ServerDiscoPublic, PresharedKey string
	RegionID                                      int64
	Region                                        []*savedAddressRelay
}

func addressBytes(value, prefix string, required bool) ([]byte, error) {
	if value == "" && !required {
		return nil, nil
	}
	raw, ok := strings.CutPrefix(value, prefix)
	if !ok {
		return nil, errors.New("invalid connection key format")
	}
	b, e := hex.DecodeString(raw)
	if e != nil || len(b) != 32 {
		return nil, errors.New("invalid connection key length or encoding")
	}
	if !required && bytes.Equal(b, make([]byte, 32)) {
		return nil, nil
	}
	return b, nil
}
func savedKeyAddress(k Identity) addressPreview {
	if k.Role != "server" {
		return addressPreview{}
	}
	unavailable := func(note string) addressPreview { return addressPreview{AddressMode: "unavailable", AddressNote: note} }
	if len(k.Private) == 0 {
		return unavailable("A node public key alone has no saved relay/discovery/PSK metadata.")
	}
	pub, e := privatePublic(k.Private)
	if e != nil || pub != k.Public {
		return unavailable("Saved identity and connection metadata do not match.")
	}
	var doc struct{ Public savedAddressMetadata }
	if json.Unmarshal(k.Private, &doc) != nil {
		return unavailable("Saved connection metadata is invalid.")
	}
	m := doc.Public
	if m.RegionID == 0 && len(m.Region) == 0 {
		return unavailable("No relay metadata is saved for this key.")
	}
	if m.RegionID < -1 {
		return unavailable("Saved relay mode is not supported.")
	}
	p := addressPayload{Region: m.RegionID}
	p.Node, e = addressBytes(m.ServerPublic, "nodekey:", true)
	if e != nil {
		return unavailable("Saved node public key is invalid.")
	}
	p.Disco, e = addressBytes(m.ServerDiscoPublic, "discokey:", false)
	if e != nil {
		return unavailable("Saved discovery key is invalid.")
	}
	p.PSK, e = addressBytes(m.PresharedKey, "psk:", false)
	if e != nil {
		return unavailable("Saved pre-shared key metadata is invalid.")
	}
	for _, region := range m.Region {
		if region == nil {
			return unavailable("Saved relay metadata contains an empty region.")
		}
		relay := addressRelay{}
		for _, node := range region.Nodes {
			if node == nil {
				return unavailable("Saved relay metadata contains an empty node.")
			}
			if node.STUNOnly {
				continue
			}
			name := node.Name
			if node.HostName != "" {
				name = ""
			}
			relay.Nodes = append(relay.Nodes, addressRelayNode{Name: name, Host: node.HostName, Certificate: node.CertName, IPv4: node.IPv4, IPv6: node.IPv6, STUNPort: node.STUNPort, DERPPort: node.DERPPort, Insecure: node.InsecureForTests})
		}
		if len(relay.Nodes) == 0 {
			return unavailable("Saved region contains no relay nodes.")
		}
		p.Relays = append(p.Relays, relay)
	}
	data, e := cbor.Marshal(p)
	if e != nil {
		return unavailable("Saved address cannot be encoded.")
	}
	mode, note := "fixed-region", "Saved key address; showing it does not start the server."
	if len(m.Region) > 0 {
		mode = "embedded-relay"
	}
	if m.RegionID == -1 && len(m.Region) == 0 {
		mode = "auto-region-template"
		note = "Relay is selected at server startup. This saved template may differ from the live address; inspect Services for the running address."
	}
	return addressPreview{Address: "tc" + base64.RawURLEncoding.EncodeToString(data), AddressMode: mode, AddressNote: note}
}
