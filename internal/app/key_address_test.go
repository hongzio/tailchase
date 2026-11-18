package app

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fxamacker/cbor/v2"
)

func addressFixture(t *testing.T, region int) Identity {
	t.Helper()
	k := testIdentity("server", 51)
	var doc map[string]any
	_ = json.Unmarshal(k.Private, &doc)
	pub := doc["Public"].(map[string]any)
	pub["ServerDiscoPublic"] = "discokey:" + strings.Repeat("ab", 32)
	pub["PresharedKey"] = "psk:" + strings.Repeat("cd", 32)
	pub["RegionID"] = region
	k.Private, _ = json.Marshal(doc)
	return k
}
func TestKeyAddressPreviewContainsOnlySavedConnectionFields(t *testing.T) {
	k := addressFixture(t, 1)
	preview := savedKeyAddress(k)
	if preview.AddressMode != "fixed-region" || !strings.HasPrefix(preview.Address, "tc") {
		t.Fatal("saved server address missing")
	}
	raw, e := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(preview.Address, "tc"))
	if e != nil {
		t.Fatal(e)
	}
	var wire map[string]any
	if e = cbor.Unmarshal(raw, &wire); e != nil {
		t.Fatal(e)
	}
	if len(wire) != 4 || len(wire["p"].([]byte)) != 32 || len(wire["k"].([]byte)) != 32 || len(wire["q"].([]byte)) != 32 {
		t.Fatal("address is missing connection metadata or includes extra fields")
	}
	if bytes.Contains(raw, []byte("privkey:")) {
		t.Fatal("private identity included in address")
	}
	auto := savedKeyAddress(addressFixture(t, -1))
	if auto.Address == "" || auto.AddressMode != "auto-region-template" || !strings.Contains(auto.AddressNote, "startup") {
		t.Fatal("auto relay address presented as a fixed live address")
	}
}
func TestKeyShowDisplaysAddressWithoutServeOrPrivateIdentity(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	k := addressFixture(t, 2)
	if e := s.Update(func(c *Config) error { c.Keys["host"] = k; return nil }); e != nil {
		t.Fatal(e)
	}
	original, _ := os.ReadFile(filepath.Join(s.Dir, "config.json"))
	var out bytes.Buffer
	if e := runWithStore(s, []string{"key", "show", "host"}, strings.NewReader(""), &out); e != nil {
		t.Fatal(e)
	}
	var shown map[string]any
	if e := json.Unmarshal(out.Bytes(), &shown); e != nil {
		t.Fatal(e)
	}
	if _, ok := shown["private"]; ok {
		t.Fatal("private key shown without explicit option")
	}
	if shown["address"] == nil || shown["address_mode"] != "fixed-region" {
		t.Fatal("server address not included")
	}
	after, _ := os.ReadFile(filepath.Join(s.Dir, "config.json"))
	if !bytes.Equal(original, after) {
		t.Fatal("viewing server address modified configuration")
	}
	if _, e := os.Stat(filepath.Join(s.Dir, "runtime.json")); !os.IsNotExist(e) {
		t.Fatal("viewing address started a daemon")
	}
	c, _ := s.Load()
	m := newTUI(s, c)
	m.tab = 2
	if strings.Contains(m.View().Content, savedKeyAddress(k).Address) {
		t.Fatal("server address leaked in normal key list")
	}
	m, cmd := press(m, 'v')
	next, _ := m.Update(cmd())
	m = next.(tuiModel)
	if m.result == nil || !strings.Contains(m.result.content, "address") {
		t.Fatal("Keys v does not show saved server address")
	}
}
func TestKeyAddressUnavailableForNodeOnlyOrMalformedMetadata(t *testing.T) {
	k := Identity{Role: "server", Public: testIdentity("client", 52).Public}
	if p := savedKeyAddress(k); p.Address != "" || p.AddressMode != "unavailable" {
		t.Fatal("nodekey-only identity fabricated a server address")
	}
	k = addressFixture(t, 1)
	var doc map[string]any
	_ = json.Unmarshal(k.Private, &doc)
	doc["Public"].(map[string]any)["PresharedKey"] = "INVALID_SECRET"
	k.Private, _ = json.Marshal(doc)
	p := savedKeyAddress(k)
	if p.Address != "" || strings.Contains(p.AddressNote, "INVALID_SECRET") {
		t.Fatal("invalid metadata encoded or leaked")
	}
	k = addressFixture(t, 1)
	_ = json.Unmarshal(k.Private, &doc)
	doc["Public"].(map[string]any)["Region"] = []any{nil}
	k.Private, _ = json.Marshal(doc)
	if savedKeyAddress(k).Address != "" {
		t.Fatal("null region encoded")
	}
}
func TestSavedAddressMatchesInstalledTailcatGenkeyAndParse(t *testing.T) {
	tool, e := exec.LookPath("tailcat")
	if e != nil {
		t.Skip("tailcat is not installed")
	}
	for _, test := range []struct {
		name, region, mode string
		noPSK              bool
	}{{"fixed", "1", "fixed-region", false}, {"auto", "auto", "auto-region-template", false}, {"embedded", "relay.example.invalid", "embedded-relay", false}, {"no-psk", "1", "fixed-region", true}} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "server.private.json")
			args := []string{"genkey", "--key=" + path, "--region=" + test.region}
			if test.noPSK {
				args = append(args, "--psk=false")
			}
			expected, e := exec.Command(tool, args...).Output()
			if e != nil {
				t.Fatal("isolated tailcat key generation failed", e)
			}
			raw, e := os.ReadFile(path)
			if e != nil {
				t.Fatal(e)
			}
			pub, e := privatePublic(raw)
			if e != nil {
				t.Fatal(e)
			}
			preview := savedKeyAddress(Identity{Role: "server", Public: pub, Private: raw})
			if preview.Address != strings.TrimSpace(string(expected)) || preview.AddressMode != test.mode {
				t.Fatal("offline address differs from tailcat genkey or has wrong mode")
			}
			parsed, e := exec.Command(tool, "parse", preview.Address).Output()
			if e != nil {
				t.Fatal("tailcat rejected offline address", e)
			}
			var decoded struct{ ServerPublic string }
			if json.Unmarshal(parsed, &decoded) != nil || decoded.ServerPublic != pub {
				t.Fatal("address decodes to wrong server identity")
			}
		})
	}
}

func TestEmbeddedAddressRetainsRelayDetails(t *testing.T) {
	tool, e := exec.LookPath("tailcat")
	if e != nil {
		t.Skip("tailcat is not installed")
	}
	k := addressFixture(t, 0)
	var doc map[string]any
	_ = json.Unmarshal(k.Private, &doc)
	doc["Public"].(map[string]any)["Region"] = []any{map[string]any{"RegionID": 7, "RegionCode": "omit-code", "RegionName": "omit-name", "Nodes": []any{map[string]any{"Name": "omit-name", "RegionID": 7, "HostName": "relay.example.invalid", "CertName": "cert.example.invalid", "IPv4": "192.0.2.1", "IPv6": "2001:db8::1", "DERPPort": 8443, "STUNPort": 3478, "InsecureForTests": true}, map[string]any{"HostName": "stun.example.invalid", "STUNOnly": true}}}}
	k.Private, _ = json.Marshal(doc)
	p := savedKeyAddress(k)
	if p.AddressMode != "embedded-relay" {
		t.Fatal("embedded relay unavailable")
	}
	output, e := exec.Command(tool, "parse", p.Address).Output()
	if e != nil {
		t.Fatal("tailcat rejected embedded address", e)
	}
	var decoded struct {
		Region []struct {
			RegionID               int
			RegionCode, RegionName string
			Nodes                  []struct {
				Name, HostName, CertName, IPv4, IPv6 string
				DERPPort, STUNPort, RegionID         int
				InsecureForTests                     bool
			}
		}
	}
	if json.Unmarshal(output, &decoded) != nil || len(decoded.Region) != 1 || len(decoded.Region[0].Nodes) != 1 {
		t.Fatal("embedded relay nodes incorrect")
	}
	r := decoded.Region[0]
	n := r.Nodes[0]
	if r.RegionID != 0 || r.RegionCode != "" || r.RegionName != "" || n.Name != "" || n.RegionID != 0 {
		t.Fatal("redundant relay metadata not omitted")
	}
	if n.HostName != "relay.example.invalid" || n.CertName != "cert.example.invalid" || n.DERPPort != 8443 || n.STUNPort != 3478 || n.IPv4 != "192.0.2.1" || n.IPv6 != "2001:db8::1" || !n.InsecureForTests {
		t.Fatal("relay connection fields lost")
	}
}
